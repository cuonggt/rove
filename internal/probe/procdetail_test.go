package probe

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestValidatePID(t *testing.T) {
	for _, pid := range []int{0, -1, -999, 1 << 23} {
		if err := ValidatePID(pid); !errors.Is(err, ErrUnsafePID) {
			t.Errorf("ValidatePID(%d) = %v, want ErrUnsafePID", pid, err)
		}
	}
	for _, pid := range []int{1, 42, 99999} {
		if err := ValidatePID(pid); err != nil {
			t.Errorf("ValidatePID(%d) = %v", pid, err)
		}
	}
}

// Captured from a real host as root: everything readable.
func TestGoldenProcRoot(t *testing.T) {
	raw, err := os.ReadFile("testdata/proc/pid1-root.txt")
	if err != nil {
		t.Skip("fixture missing")
	}
	p, err := ParseProcDetail(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Found() || p.PID != 1 {
		t.Fatalf("pid = %d err = %q", p.PID, p.Err)
	}
	if p.Comm == "" || p.Cmdline == "" {
		t.Errorf("comm=%q cmdline=%q", p.Comm, p.Cmdline)
	}
	if p.User != "root" || p.UID != 0 {
		t.Errorf("user = %q uid = %d", p.User, p.UID)
	}
	if p.Exe == "" || p.Cwd == "" {
		t.Error("root can read exe and cwd")
	}
	if !p.HasFDs || p.FDs <= 0 {
		t.Error("root can count open files")
	}
	if p.Limited {
		t.Error("root read everything; limited should not be set")
	}
	if p.StateLabel() == "" {
		t.Error("state should read as a word, not a letter")
	}
	// The fixture is itself a container, so pid 1 belongs to one.
	if !p.InContainer() || len(p.ShortContainer()) != 12 {
		t.Errorf("container = %q", p.Container)
	}
}

// The same process seen by an account that cannot read another user's
// symlinks. Missing fields must be reported as missing, not as empty.
func TestGoldenProcUnprivileged(t *testing.T) {
	raw, err := os.ReadFile("testdata/proc/pid1-unprivileged.txt")
	if err != nil {
		t.Skip("fixture missing")
	}
	p, err := ParseProcDetail(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Found() {
		t.Fatalf("err = %q", p.Err)
	}
	// What /proc exposes to everyone still comes through.
	if p.Comm == "" || p.User == "" || p.Threads == 0 {
		t.Errorf("basic status missing: %+v", p)
	}
	// What it does not must be flagged rather than shown as blank.
	if !p.Limited {
		t.Error("unreadable fields must set limited")
	}
	if p.Exe != "" || p.Cwd != "" || p.HasFDs {
		t.Errorf("fields this account cannot read should be absent: exe=%q cwd=%q fds=%v", p.Exe, p.Cwd, p.HasFDs)
	}
}

// A pid from a list taken seconds ago may already be gone, and that is a
// normal answer rather than a failure.
func TestExitedProcessIsExplained(t *testing.T) {
	p, err := ParseProcDetail([]byte("rove-proc 1\nproc.pid=4242\nproc.error=no process 4242; it may have exited since the list was taken\n"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Found() {
		t.Error("a missing process is not found")
	}
	if !strings.Contains(p.Err, "exited") {
		t.Errorf("err = %q", p.Err)
	}
}

func TestZombieIsRecognised(t *testing.T) {
	p, _ := ParseProcDetail([]byte("rove-proc 1\nproc.pid=9\nproc.state=Z\nproc.state_text=zombie\n"))
	if !p.Zombie() {
		t.Error("Z means zombie, and the fix is with the parent")
	}
	if p.StateLabel() != "zombie (Z)" {
		t.Errorf("label = %q", p.StateLabel())
	}
}

// The environment is never collected, and no amount of parsing should be
// able to surface it.
func TestEnvironmentIsNeverCollected(t *testing.T) {
	if strings.Contains(ProcDetailScript, "/environ") {
		// The only mention allowed is the comment explaining the refusal.
		for _, line := range strings.Split(ProcDetailScript, "\n") {
			if strings.Contains(line, "/environ") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
				t.Errorf("environ is read on: %s", line)
			}
		}
	}
}
