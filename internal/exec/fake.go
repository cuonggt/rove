package exec

import (
	"context"
	"time"

	"github.com/cuonggt/rove/internal/model"
)

// Fake is an Executor for tests. Responses are keyed by alias; an alias with
// no entry returns ErrDefault, so a test must be explicit about every host
// it expects to be reached.
type Fake struct {
	Responses map[string]FakeResponse
	Default   FakeResponse
}

type FakeResponse struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
	Delay    time.Duration
}

func (f *Fake) Run(ctx context.Context, t model.Target, c Command) (Result, error) {
	r, ok := f.Responses[t.Alias]
	if !ok {
		r = f.Default
	}
	if r.Delay > 0 {
		select {
		case <-time.After(r.Delay):
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	return Result{
		Stdout:   []byte(r.Stdout),
		Stderr:   []byte(r.Stderr),
		ExitCode: r.ExitCode,
	}, r.Err
}

var _ Executor = (*Fake)(nil)
