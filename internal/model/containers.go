package model

import (
	"sort"
	"strings"
)

// Container is one entry from a container runtime's own listing.
type Container struct {
	ID     string
	State  string // running, exited, created, paused
	Name   string
	Image  string
	Status string // the runtime's phrasing: "Up 2 hours", "Exited (1) ..."
	Ports  string // the runtime's published-port spec, verbatim
}

func (c Container) Running() bool { return c.State == "running" }

// ShortID is the twelve characters people actually paste.
func (c Container) ShortID() string {
	if len(c.ID) > 12 {
		return c.ID[:12]
	}
	return c.ID
}

// Exposed reports a container publishing a port on every interface. This is
// the same judgment the ports screen makes, and it matters more here: a
// published container port bypasses the host firewall rules people expect
// to be protecting them.
func (c Container) Exposed() bool {
	return strings.Contains(c.Ports, "0.0.0.0:") || strings.Contains(c.Ports, ":::")
}

// ContainerList is what one host's container runtime reported.
type ContainerList struct {
	// CLI is the binary that was found, even when it could not be used.
	// "docker is installed but its daemon is not reachable" is a different
	// and more useful statement than "no docker".
	CLI        string
	Source     string // docker, podman, nerdctl, none
	Version    string
	Containers []Container
	Err        string
}

// Available reports whether the runtime could actually be queried.
func (l ContainerList) Available() bool {
	return l.Source != "" && l.Source != "none"
}

// Installed reports that a runtime binary exists, whether or not it worked.
func (l ContainerList) Installed() bool { return l.CLI != "" }

// RunningCount is the number people mean by "how many containers".
func (l ContainerList) RunningCount() int {
	n := 0
	for _, c := range l.Containers {
		if c.Running() {
			n++
		}
	}
	return n
}

// ExposedCount is how many containers publish a port to the network.
func (l ContainerList) ExposedCount() int {
	n := 0
	for _, c := range l.Containers {
		if c.Running() && c.Exposed() {
			n++
		}
	}
	return n
}

// Ordered puts running containers first, then the ones that stopped. A
// stopped container is usually why somebody opened this screen, so it sorts
// above created and paused rather than being buried.
func (l ContainerList) Ordered() []Container {
	out := append([]Container(nil), l.Containers...)
	sort.SliceStable(out, func(i, j int) bool {
		if r := containerRank(out[i]); r != containerRank(out[j]) {
			return r < containerRank(out[j])
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func containerRank(c Container) int {
	switch c.State {
	case "running":
		return 0
	case "exited", "dead":
		return 1
	case "paused":
		return 2
	default:
		return 3
	}
}
