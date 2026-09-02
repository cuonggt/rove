package model

import (
	"sort"
	"strings"
)

// Listener is one socket accepting connections.
type Listener struct {
	Proto   string // tcp, udp
	Addr    string
	Port    int
	PID     int
	Process string
	// HasProcess is false when the owner could not be read, which is the
	// normal case for an unprivileged account looking at another user's
	// socket. A blank owner is not the same claim as "no process".
	HasProcess bool
}

// Exposed reports a socket bound to every interface rather than to
// loopback. This is the distinction the raw tools do not make and the one
// that actually matters: a database on 127.0.0.1 and the same database on
// 0.0.0.0 are different situations.
func (l Listener) Exposed() bool {
	switch l.Addr {
	case "0.0.0.0", "::", "*", "":
		return true
	}
	return false
}

// Loopback reports a socket only reachable from the host itself.
func (l Listener) Loopback() bool {
	return l.Addr == "127.0.0.1" || l.Addr == "::1" || strings.HasPrefix(l.Addr, "127.")
}

// ConnState is a count of connections in one TCP state.
type ConnState struct {
	State string
	Count int
}

// PortList is what one host is listening on, plus a summary of its
// connections. The connections are counted rather than listed: a busy host
// has tens of thousands and the count is the useful part.
type PortList struct {
	Source string // ss, netstat, none
	// Limited means process owners are missing because the account is not
	// root. The ports are still complete.
	Limited   bool
	Listeners []Listener
	Conns     []ConnState
	Err       string
}

func (p PortList) Available() bool {
	return p.Source != "" && p.Source != "none"
}

// Established is the count people mean by "how busy is it".
func (p PortList) Established() int {
	for _, c := range p.Conns {
		if c.State == "ESTAB" || c.State == "ESTABLISHED" {
			return c.Count
		}
	}
	return 0
}

// ExposedCount is how many sockets face the network.
func (p PortList) ExposedCount() int {
	n := 0
	for _, l := range p.Listeners {
		if l.Exposed() {
			n++
		}
	}
	return n
}

// Ordered puts the sockets facing the network first, then sorts by port.
// The question behind "what is listening" is almost always "what can be
// reached from outside", so that half of the list goes at the top.
func (p PortList) Ordered() []Listener {
	out := append([]Listener(nil), p.Listeners...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Exposed() != out[j].Exposed() {
			return out[i].Exposed()
		}
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Proto < out[j].Proto
	})
	return out
}

// SortedConns returns connection states in a stable, useful order rather
// than whatever order the shell's associative array produced.
func (p PortList) SortedConns() []ConnState {
	out := append([]ConnState(nil), p.Conns...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].State < out[j].State
	})
	return out
}
