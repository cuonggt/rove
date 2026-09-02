// Package model is the shared vocabulary. It must not import any other
// internal package, so that every layer can speak it without a cycle.
package model

// Source records where a server came from. v0.1 populates ssh-config and
// file; the field exists so that adding a source later is not a rewrite.
type Source string

const (
	SourceSSHConfig Source = "ssh-config"
	SourceFile      Source = "file"
)

// Connection records how we reach a server. v0.1 only ever sets ssh.
type Connection string

const (
	ConnSSH Connection = "ssh"
)

// Target is what an Executor addresses. It is a struct rather than a string
// because a non-SSH transport needs more than a name to find a host.
type Target struct {
	Alias string
	Conn  Connection
}

// Meta holds everything about a server that is not needed to connect to it.
type Meta struct {
	Env           string
	Tags          []string
	ProxyJump     string
	IdentityFiles []string
	// HardwareKey is set when a resolved identity looks like a FIDO or
	// PKCS#11 token. Those serialise agent signatures, so the caller
	// lowers concurrency rather than queueing dozens of touch prompts.
	HardwareKey bool
}

// Server is one machine we can reach.
type Server struct {
	Name    string // the ssh alias, and the user-facing identity
	Address string // resolved HostName
	User    string
	Port    string
	Source  Source
	Conn    Connection
	Meta    Meta
}

func (s Server) Target() Target {
	return Target{Alias: s.Name, Conn: s.Conn}
}

// Status is the terminal state of an attempt to reach a server. Every value
// other than StatusOK carries a Reason, and most carry a Fix, because
// "unreachable" on its own is not something a person can act on.
type Status string

const (
	StatusUnknown    Status = "unknown"
	StatusOK         Status = "ok"
	StatusTimeout    Status = "timeout"
	StatusDNS        Status = "dns"
	StatusRefused    Status = "refused"
	StatusAuth       Status = "auth"
	StatusHostKey    Status = "hostkey"
	StatusProbeError Status = "probe-error"
)

// Outcome is one attempt against one server.
type Outcome struct {
	Server Server
	Status Status
	Reason string // human-readable, always set when Status != StatusOK
	Fix    string // the command that resolves it, when one exists
	Stdout string
	MS     int64
}
