package model

import (
	"sort"
	"strings"
)

// Process is one row of the remote process table. Not every host can report
// every column: busybox has no percentages at all, so HasCPU and HasMem say
// whether the numbers mean anything rather than letting a zero pretend to
// be a measurement.
type Process struct {
	PID     int
	User    string
	CPU     float64
	Mem     float64
	HasCPU  bool
	HasMem  bool
	RSSKB   int64
	HasRSS  bool
	Command string
}

// ProcessList is what one host answered.
type ProcessList struct {
	// Fields names the columns the host could actually produce.
	Fields []string
	// Total is how many processes were running, which may exceed the number
	// returned: the probe caps the list so that a busy host cannot return
	// megabytes. Truncated says the cap was hit, and the interface says so
	// rather than implying the list is complete.
	Total     int
	Truncated bool
	// SortedRemotely is true when the host sorted by CPU itself. When false
	// the client sorts, and on a truncated busybox list that means the rows
	// are an arbitrary sample rather than the busiest ones.
	SortedRemotely bool
	Procs          []Process
	Err            string
}

// Unit is one service as its init system describes it.
type Unit struct {
	Name        string
	Load        string // loaded, not-found, masked
	Active      string // active, inactive, failed
	Sub         string // running, exited, dead, failed
	Description string
}

// Failed reports whether this unit is in a state a person should look at.
func (u Unit) Failed() bool {
	return u.Active == "failed" || u.Sub == "failed"
}

// Running reports whether the unit is up.
func (u Unit) Running() bool {
	return u.Active == "active" && u.Sub != "exited" || u.Sub == "running" || u.Sub == "started"
}

// Missing reports a unit the init system cannot resolve, which usually means
// a package was removed without disabling it first.
func (u Unit) Missing() bool {
	return u.Load == "not-found" || u.Load == "masked"
}

// ShortName drops the .service suffix, which is noise in a list where every
// row has it.
func (u Unit) ShortName() string {
	return strings.TrimSuffix(u.Name, ".service")
}

// ServiceList is what one host's init system reported.
type ServiceList struct {
	Init  string // systemd, openrc, unsupported
	State string // systemctl is-system-running
	Units []Unit
}

// Supported reports whether this host's init system can be read at all. An
// unsupported init is shown as such; an empty list would look like a host
// with no services, which is a different and wrong claim.
func (s ServiceList) Supported() bool {
	return s.Init != "" && s.Init != "unsupported" && s.Init != "unknown"
}

// Ordered returns the units with the ones needing attention first.
//
// The init system lists units alphabetically, which buries a failure sixty
// rows down while the footer cheerfully reports "1 failed". A list nobody
// scrolls is a list that does not do its job.
func (s ServiceList) Ordered() []Unit {
	out := append([]Unit(nil), s.Units...)
	sort.SliceStable(out, func(i, j int) bool {
		if r := unitRank(out[i]); r != unitRank(out[j]) {
			return r < unitRank(out[j])
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// unitRank orders by how much a person needs to look at it.
func unitRank(u Unit) int {
	switch {
	case u.Failed():
		return 0
	case u.Missing():
		return 1
	case u.Running():
		return 2
	default:
		return 3
	}
}

// FailedUnits returns the units worth attention, in the order reported.
func (s ServiceList) FailedUnits() []Unit {
	out := make([]Unit, 0, 4)
	for _, u := range s.Units {
		if u.Failed() {
			out = append(out, u)
		}
	}
	return out
}
