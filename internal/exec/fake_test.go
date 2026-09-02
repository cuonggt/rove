package exec

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cuonggt/rove/internal/model"
)

func TestFakeRespectsContextDeadline(t *testing.T) {
	f := &Fake{Default: FakeResponse{Delay: 2 * time.Second}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := f.Run(ctx, model.Target{Alias: "slow"}, Command{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestFakeRoutesByAlias(t *testing.T) {
	f := &Fake{
		Responses: map[string]FakeResponse{
			"good": {Stdout: "ok"},
			"bad":  {Stderr: "Permission denied (publickey).", Err: errors.New("exit status 255")},
		},
	}
	r, err := f.Run(context.Background(), model.Target{Alias: "good"}, Command{})
	if err != nil || string(r.Stdout) != "ok" {
		t.Fatalf("good: %q %v", r.Stdout, err)
	}

	r, err = f.Run(context.Background(), model.Target{Alias: "bad"}, Command{})
	if status, _, _ := Classify("bad", err, string(r.Stderr)); status != model.StatusAuth {
		t.Fatalf("bad: status = %q", status)
	}
}
