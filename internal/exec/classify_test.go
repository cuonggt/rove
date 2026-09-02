package exec

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cuonggt/rove/internal/model"
)

func TestClassify(t *testing.T) {
	boom := errors.New("exit status 255")

	cases := []struct {
		name   string
		err    error
		stderr string
		want   model.Status
		fix    bool
	}{
		{"ok", nil, "", model.StatusOK, false},
		{"deadline", context.DeadlineExceeded, "", model.StatusTimeout, false},
		{"changed key", boom,
			"@@@@@@@@@@\n@ WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED! @\n",
			model.StatusHostKey, true},
		{"unknown key", boom, "Host key verification failed.", model.StatusHostKey, true},
		{"denied", boom, "Permission denied (publickey).", model.StatusAuth, true},
		{"dns", boom, "ssh: Could not resolve hostname nope: nodename nor servname provided", model.StatusDNS, false},
		{"refused", boom, "ssh: connect to host x port 22: Connection refused", model.StatusRefused, false},
		{"timeout", boom, "ssh: connect to host x port 22: Operation timed out", model.StatusTimeout, false},
		{"no route", boom, "ssh: connect to host x port 22: No route to host", model.StatusTimeout, false},
		{"unknown", boom, "something nobody predicted", model.StatusProbeError, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason, fix := Classify("web-01", c.err, c.stderr)
			if got != c.want {
				t.Fatalf("status = %q, want %q", got, c.want)
			}
			if c.want != model.StatusOK && reason == "" {
				t.Error("a failure must always carry a reason")
			}
			if c.fix && fix == "" {
				t.Error("this failure has a known fix and should offer it")
			}
			if c.fix && !strings.Contains(fix, "web-01") {
				t.Errorf("fix should name the host, got %q", fix)
			}
		})
	}
}

// A banner line is noise; the diagnostic underneath it is the useful part.
func TestProbeErrorSkipsBannerLines(t *testing.T) {
	_, reason, _ := Classify("x", errors.New("boom"), "@@@@@@\n@ banner @\n@@@@@@\nthe real problem\n")
	if reason != "the real problem" {
		t.Fatalf("reason = %q", reason)
	}
}
