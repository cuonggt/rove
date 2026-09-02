package model

import "fmt"

// ProcessDetail is everything worth knowing about one process, gathered
// from /proc so that it works on a host with no tooling installed.
//
// It deliberately excludes the environment. /proc/PID/environ routinely
// holds database passwords and API keys, and a diagnostic screen is not
// worth putting those into somebody's scrollback.
type ProcessDetail struct {
	PID     int
	Comm    string
	Cmdline string

	State     string // the single letter: R, S, D, Z
	StateText string // the kernel's own word for it

	PPid   int
	Parent string

	Threads int
	UID     int
	User    string

	RSSKB    int64
	VSZKB    int64
	ElapsedS int64

	Exe string
	Cwd string

	FDs    int
	HasFDs bool

	// Container is the cgroup-derived id when this process belongs to one.
	// On a container host most processes do, and ps gives no hint of it.
	//
	// It is empty when the id cannot be seen rather than when there is no
	// container: a process running inside its own cgroup namespace reads
	// its cgroup as "0::/" and learns nothing about the container holding
	// it. Detection therefore works from the host looking in, which is how
	// rove is used, and not from inside a container looking at itself.
	Container string

	// Limited means some fields were unreadable, which for an unprivileged
	// account looking at another user's process is the normal case.
	Limited bool
	Err     string
}

func (p ProcessDetail) Found() bool { return p.Err == "" && p.PID > 0 }

func (p ProcessDetail) InContainer() bool { return p.Container != "" }

// ShortContainer is the twelve characters that match what docker ps shows.
func (p ProcessDetail) ShortContainer() string {
	if len(p.Container) > 12 {
		return p.Container[:12]
	}
	return p.Container
}

// StateLabel reads as a word rather than a letter nobody remembers.
func (p ProcessDetail) StateLabel() string {
	switch {
	case p.StateText != "" && p.State != "":
		return fmt.Sprintf("%s (%s)", p.StateText, p.State)
	case p.StateText != "":
		return p.StateText
	default:
		return p.State
	}
}

// Zombie is worth calling out: it means the parent never reaped it, so the
// fix is with the parent rather than with this process.
func (p ProcessDetail) Zombie() bool { return p.State == "Z" }
