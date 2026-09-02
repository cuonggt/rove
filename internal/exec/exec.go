// Package exec runs commands on a remote target. The Executor interface is
// the seam that a non-SSH transport would implement later; nothing above it
// knows that OpenSSH exists.
package exec

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/cuonggt/rove/internal/model"
)

// Command is what to run. Argv is empty when Stdin carries a script to be
// fed to a shell, which is how the probe is delivered.
type Command struct {
	Argv  []string
	Stdin io.Reader
}

// Result is a command that ran. A remote command exiting non-zero is a
// Result, not an error: only a transport failure is an error.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Duration time.Duration
}

type Executor interface {
	Run(ctx context.Context, t model.Target, c Command) (Result, error)
}

// TransportError is a failure to reach the host at all, as opposed to a
// command that ran and exited non-zero. It carries ssh's own diagnostic,
// because that text is the only thing that distinguishes a timeout from a
// refused key from a changed host key.
type TransportError struct {
	Err    error
	Stderr string
}

func (e *TransportError) Error() string {
	if line := firstLine(e.Stderr); line != "" {
		return line
	}
	return e.Err.Error()
}

func (e *TransportError) Unwrap() error { return e.Err }

// StderrOf recovers the remote diagnostic from an error chain, if there is
// one. Anything else yields the empty string, and Classify falls back to the
// error text.
func StderrOf(err error) string {
	var te *TransportError
	if errors.As(err, &te) {
		return te.Stderr
	}
	return ""
}
