// Package action changes things on a remote host.
//
// It is deliberately a separate package from probe. Everything in probe is
// read-only and a test enforces that; putting a single writing command in
// there would turn a guarantee into a habit. Nothing here is imported by
// probe, and nothing in probe grows a verb.
package action

import (
	"fmt"
	"strconv"
	"strings"
)

// Risk is how much a person should think before pressing the key.
type Risk int

const (
	// RiskWrite changes running state and can be undone by the opposite
	// action: starting what was stopped, restarting what is running.
	RiskWrite Risk = iota
	// RiskDangerous can lose work, drop the connection rove itself is
	// using, or leave a machine unreachable. It always asks first, and the
	// question names the specific consequence rather than saying "are you
	// sure".
	RiskDangerous
)

func (r Risk) String() string {
	if r == RiskDangerous {
		return "dangerous"
	}
	return "write"
}

// Kind is the closed set of things rove can do. It is an enum rather than a
// command string because a command string is an injection waiting to
// happen: the only verbs that reach a host are the ones written here.
type Kind string

const (
	ServiceStart   Kind = "service.start"
	ServiceStop    Kind = "service.stop"
	ServiceRestart Kind = "service.restart"

	ProcessTerm Kind = "process.term"
	ProcessKill Kind = "process.kill"

	ContainerStart   Kind = "container.start"
	ContainerStop    Kind = "container.stop"
	ContainerRestart Kind = "container.restart"
)

// spec describes one verb: its risk, and what to warn about.
type spec struct {
	risk Risk
	verb string
	// consequence is the specific thing that can go wrong, shown in the
	// confirmation. "This may drop your connection" is useful; "are you
	// sure" is not.
	consequence string
}

var specs = map[Kind]spec{
	ServiceStart:   {RiskWrite, "service-start", ""},
	ServiceRestart: {RiskWrite, "service-restart", "in-flight requests to this service will be dropped"},
	ServiceStop:    {RiskDangerous, "service-stop", "nothing will start it again until you do"},

	ProcessTerm: {RiskWrite, "process-term", "the process is asked to exit and may refuse"},
	ProcessKill: {RiskDangerous, "process-kill", "the process cannot save or clean up; unwritten data is lost"},

	ContainerStart:   {RiskWrite, "container-start", ""},
	ContainerRestart: {RiskWrite, "container-restart", "connections to this container will be dropped"},
	ContainerStop:    {RiskDangerous, "container-stop", "nothing will start it again until you do"},
}

// Action is one thing to do to one target.
type Action struct {
	Kind   Kind
	Target string
	// Label is what the target is called in the interface, which is not
	// always what it is called on the host: a container is addressed by id
	// and recognised by name.
	Label string
}

func (a Action) spec() (spec, bool) { s, ok := specs[a.Kind]; return s, ok }

func (a Action) Risk() Risk {
	s, ok := a.spec()
	if !ok {
		return RiskDangerous // an unknown verb is never the safe one
	}
	return s.risk
}

func (a Action) Dangerous() bool { return a.Risk() == RiskDangerous }

// Consequence is the specific thing that can go wrong, or empty.
func (a Action) Consequence() string {
	s, _ := a.spec()
	return s.consequence
}

func (a Action) name() string {
	if a.Label != "" {
		return a.Label
	}
	return a.Target
}

// Summary is the sentence shown before doing it, in the imperative, naming
// the host so a confirmation can never be about the wrong machine.
func (a Action) Summary(host string) string {
	verb := map[Kind]string{
		ServiceStart: "Start", ServiceStop: "Stop", ServiceRestart: "Restart",
		ProcessTerm: "Terminate", ProcessKill: "Force kill",
		ContainerStart: "Start", ContainerStop: "Stop", ContainerRestart: "Restart",
	}[a.Kind]
	if verb == "" {
		verb = string(a.Kind)
	}

	what := a.name()
	switch a.Kind {
	case ProcessTerm, ProcessKill:
		what = "process " + what
	case ContainerStart, ContainerStop, ContainerRestart:
		what = "container " + what
	}
	return fmt.Sprintf("%s %s on %s", verb, what, host)
}

// Validate rejects anything that could not have come from this host's own
// listings. Targets reach a remote shell through ssh's argv concatenation,
// exactly as they do for the read-only probes.
func (a Action) Validate() error {
	if _, ok := a.spec(); !ok {
		return fmt.Errorf("unknown action %q", a.Kind)
	}
	if a.Target == "" {
		return fmt.Errorf("%s: no target", a.Kind)
	}

	switch a.Kind {
	case ProcessTerm, ProcessKill:
		pid, err := strconv.Atoi(a.Target)
		if err != nil || pid <= 1 || pid > 1<<22 {
			// pid 1 is excluded on purpose: killing init takes the machine
			// down, and no interface should make that a keystroke.
			return fmt.Errorf("%s: %q is not a process this may signal", a.Kind, a.Target)
		}
	case ServiceStart, ServiceStop, ServiceRestart:
		if !safeUnitName(a.Target) {
			return fmt.Errorf("%s: %q is not a unit name", a.Kind, a.Target)
		}
	case ContainerStart, ContainerStop, ContainerRestart:
		if !safeContainerID(a.Target) {
			return fmt.Errorf("%s: %q is not a container id", a.Kind, a.Target)
		}
	}
	return nil
}

func safeUnitName(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-' || r == '@' || r == ':' || r == '+':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// safeContainerID accepts an id or a name, both of which runtimes restrict
// to a conservative alphabet.
func safeContainerID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	return strings.IndexFunc(s, func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return false
		case r == '.' || r == '_' || r == '-':
			return false
		}
		return true
	}) < 0
}
