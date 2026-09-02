package fleet

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	rexec "github.com/cuonggt/rove/internal/exec"
	"github.com/cuonggt/rove/internal/model"
)

func servers(names ...string) []model.Server {
	out := make([]model.Server, len(names))
	for i, n := range names {
		out[i] = model.Server{Name: n, Source: model.SourceSSHConfig, Conn: model.ConnSSH}
	}
	return out
}

// probeOut builds probe output with the fields a test cares about.
func probeOut(cpuIdle int, extra ...string) string {
	var b strings.Builder
	b.WriteString("rove-probe 1\n")
	b.WriteString("sys.kind=linux\nsys.os=Test Linux\nsys.kernel=6.0\n")
	b.WriteString("cpu.cores=4\n")
	b.WriteString("load=0.50 0.40 0.30\n")
	b.WriteString("mem.total_kb=1000\nmem.available_kb=400\n")
	b.WriteString("fs=/dev/sda1 1000 380 /\n")
	b.WriteString("svc.init=systemd\n")
	b.WriteString("cpu.stat=cpu  1000 0 0 " + itoa(cpuIdle) + " 0 0 0 0\n")
	for _, e := range extra {
		b.WriteString(e + "\n")
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

func newFleet(f *rexec.Fake, names ...string) *Fleet {
	return New(f, servers(names...), 4, 2*time.Second)
}

func TestHealthyHostParses(t *testing.T) {
	f := newFleet(&rexec.Fake{Default: rexec.FakeResponse{Stdout: probeOut(9000)}}, "web-01")
	f.RefreshAll(context.Background())

	h := f.Hosts()[0]
	if h.Status != model.StatusOK {
		t.Fatalf("status = %q (%s)", h.Status, h.Reason)
	}
	if pct, ok := h.Snap.MemUsedPercent(); !ok || pct != 60 {
		t.Errorf("mem = %.0f%%", pct)
	}
	if h.Note() != "" {
		t.Errorf("a healthy host should be quiet, got note %q", h.Note())
	}
	if h.NeedsAttention() {
		t.Error("a healthy host should not need attention")
	}
}

// CPU is a delta. The first refresh has nothing to compare against, and
// reporting 0% there would be a lie rather than a missing value.
func TestCPUAppearsOnSecondRefresh(t *testing.T) {
	fake := &rexec.Fake{Default: rexec.FakeResponse{Stdout: probeOut(9000)}}
	f := newFleet(fake, "web-01")

	f.RefreshAll(context.Background())
	if f.Hosts()[0].HasCPU {
		t.Fatal("first sample cannot yield a percentage")
	}

	// 1000 more busy ticks, 1000 more idle: 50% busy over the interval.
	fake.Default = rexec.FakeResponse{Stdout: "rove-probe 1\ncpu.stat=cpu  2000 0 0 10000 0 0 0 0\n"}
	f.RefreshAll(context.Background())

	h := f.Hosts()[0]
	if !h.HasCPU {
		t.Fatal("second sample should yield a percentage")
	}
	if h.CPUPct < 49.9 || h.CPUPct > 50.1 {
		t.Errorf("cpu = %.2f%%, want 50", h.CPUPct)
	}
}

// A host that stops answering keeps its last figures. Blanking them throws
// away the most useful thing available at the moment something breaks.
func TestFailureKeepsLastKnownFigures(t *testing.T) {
	fake := &rexec.Fake{Default: rexec.FakeResponse{Stdout: probeOut(9000)}}
	f := newFleet(fake, "web-01")
	f.RefreshAll(context.Background())

	fake.Default = rexec.FakeResponse{
		Err: &rexec.TransportError{
			Err:    errors.New("exit status 255"),
			Stderr: "ssh: connect to host web-01 port 22: Operation timed out",
		},
	}
	f.RefreshAll(context.Background())

	h := f.Hosts()[0]
	if h.Status != model.StatusTimeout {
		t.Fatalf("status = %q, want timeout", h.Status)
	}
	if !h.HasSnap || h.Snap.OS != "Test Linux" {
		t.Error("last known snapshot should be retained")
	}
	if !h.Stale() {
		t.Error("retained figures must be marked stale")
	}
	if !strings.Contains(h.Note(), "timed out") || !strings.Contains(h.Note(), "ago") {
		t.Errorf("note = %q, want the reason and an age", h.Note())
	}
}

// The gateway case: ssh connects, the command exits, nothing usable comes
// back. Reporting that host as healthy is the bug this locks down.
func TestConnectedButNoShellIsNotHealthy(t *testing.T) {
	f := newFleet(&rexec.Fake{Default: rexec.FakeResponse{Stdout: "", ExitCode: 1}}, "gateway")
	f.RefreshAll(context.Background())

	h := f.Hosts()[0]
	if h.Status == model.StatusOK {
		t.Fatal("a host that ran no shell must not read as healthy")
	}
	if h.Status != model.StatusProbeError {
		t.Errorf("status = %q, want probe-error", h.Status)
	}
	if !strings.Contains(h.Fix, "gateway") {
		t.Errorf("fix should name the host, got %q", h.Fix)
	}
}

func TestAuthFailureOffersItsFix(t *testing.T) {
	f := newFleet(&rexec.Fake{Default: rexec.FakeResponse{
		Err: &rexec.TransportError{
			Err:    errors.New("exit status 255"),
			Stderr: "tester@127.0.0.1: Permission denied (publickey).",
		},
	}}, "locked")
	f.RefreshAll(context.Background())

	h := f.Hosts()[0]
	if h.Status != model.StatusAuth {
		t.Fatalf("status = %q", h.Status)
	}
	if h.Fix == "" {
		t.Error("an auth failure has a known fix and should offer it")
	}
}

// The note is the product. Its precedence has to be deliberate: a failed
// unit matters more than a full disk, which matters more than high load.
func TestNotePrecedence(t *testing.T) {
	cases := []struct {
		name  string
		extra []string
		want  string
	}{
		{"failed unit wins", []string{"svc.failed=backup.service", "fs=/dev/sda2 100 95 /var"}, "backup.service failed"},
		{"several failed units", []string{"svc.failed=a.service", "svc.failed=b.service"}, "2 failed units"},
		{"full disk", []string{"fs=/dev/sda2 100 91 /var"}, "/var 91% full"},
		{"high load", []string{"load=9.2 8 7"}, "load 2.3x cores"},
		{"quiet when healthy", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFleet(&rexec.Fake{Default: rexec.FakeResponse{Stdout: probeOut(9000, c.extra...)}}, "h")
			f.RefreshAll(context.Background())
			if got := f.Hosts()[0].Note(); got != c.want {
				t.Errorf("note = %q, want %q", got, c.want)
			}
		})
	}
}

// One slow host must not hold up the rest of the table.
func TestOneSlowHostDoesNotBlockTheFleet(t *testing.T) {
	fake := &rexec.Fake{
		Responses: map[string]rexec.FakeResponse{
			"slow": {Delay: 10 * time.Second},
			"fast": {Stdout: probeOut(9000)},
		},
	}
	f := New(fake, servers("slow", "fast"), 4, 200*time.Millisecond)

	start := time.Now()
	f.RefreshAll(context.Background())
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("fleet refresh took %v; the slow host blocked it", elapsed)
	}

	byName := map[string]Host{}
	for _, h := range f.Hosts() {
		byName[h.Server.Name] = h
	}
	if byName["fast"].Status != model.StatusOK {
		t.Errorf("fast host: %q", byName["fast"].Status)
	}
	if byName["slow"].Status != model.StatusTimeout {
		t.Errorf("slow host: %q, want timeout", byName["slow"].Status)
	}
}
