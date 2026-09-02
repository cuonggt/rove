package probe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cuonggt/rove/internal/model"
)

// Every fixture in testdata is real output captured from that distribution.
// Adding a distro means dropping its capture in; this test picks it up.
func TestGoldenFixtures(t *testing.T) {
	files, err := filepath.Glob("testdata/snapshot/*.txt")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures found: %v", err)
	}

	for _, f := range files {
		t.Run(strings.TrimSuffix(filepath.Base(f), ".txt"), func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			s, err := Parse(raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if s.Kind != "linux" {
				t.Errorf("kind = %q, want linux", s.Kind)
			}
			if s.OS == "" {
				t.Error("no OS name")
			}
			if s.Kernel == "" {
				t.Error("no kernel")
			}
			if !s.HasCPU || s.CPU.Total() == 0 {
				t.Error("no usable cpu counters")
			}
			if !s.HasLoad {
				t.Error("no load average")
			}
			if s.MemTotalKB <= 0 {
				t.Error("no memory total")
			}
			if pct, ok := s.MemUsedPercent(); !ok || pct < 0 || pct > 100 {
				t.Errorf("memory used = %.1f%% (ok=%v)", pct, ok)
			}
			if s.Init == "" {
				t.Error("init system not reported")
			}
			// A host whose init cannot be queried must say so, so that an
			// empty failed-unit list is never mistaken for a clean bill.
			if s.Init == "systemd" && s.SvcQuery == "" {
				t.Error("systemd host did not report whether it could be queried")
			}

			// Filtering must leave a root filesystem and drop the noise.
			root, ok := s.FullestFilesystem()
			if !ok {
				t.Fatal("no real filesystem survived filtering")
			}
			if root.UsedPercent() < 0 || root.UsedPercent() > 100 {
				t.Errorf("%s used = %.1f%%", root.Mount, root.UsedPercent())
			}
			for _, fs := range s.RealFilesystems() {
				if strings.HasPrefix(fs.Mount, "/proc") || strings.HasPrefix(fs.Mount, "/etc/") {
					t.Errorf("pseudo mount survived filtering: %s on %s", fs.Mount, fs.Device)
				}
			}
		})
	}
}

func TestUbuntuFixtureExactly(t *testing.T) {
	raw, err := os.ReadFile("testdata/snapshot/ubuntu-24-04.txt")
	if err != nil {
		t.Skip("fixture missing")
	}
	s, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.OS != "Ubuntu 24.04.4 LTS" {
		t.Errorf("OS = %q", s.OS)
	}
	if s.Cores != 10 {
		t.Errorf("cores = %d", s.Cores)
	}
	// /proc/stat pads its label with two spaces; splitting by index breaks.
	if s.CPU.User != 3843618 || s.CPU.Idle != 423961705 {
		t.Errorf("cpu = %+v", s.CPU)
	}
	if s.Load != [3]float64{4.00, 2.69, 1.17} {
		t.Errorf("load = %v", s.Load)
	}
	if s.MemTotalKB != 8196572 || s.MemAvailKB != 5555772 {
		t.Errorf("mem = %d/%d", s.MemAvailKB, s.MemTotalKB)
	}

	// The container bind-mounts /etc/hosts off the host disk, which would
	// otherwise report the whole disk a second time under a config path.
	for _, fs := range s.RealFilesystems() {
		if fs.Mount == "/etc/hosts" {
			t.Error("/etc/hosts bind mount should be filtered out")
		}
	}
	// An overlay root is still the container's real disk.
	root, ok := s.FullestFilesystem()
	if !ok || root.Mount != "/" {
		t.Errorf("fullest = %+v, want /", root)
	}
}

// A host that connects but returns nothing usable must not parse as healthy.
// A gateway with no machine behind it exits cleanly and prints nothing.
func TestOutputWithoutHeaderIsAnError(t *testing.T) {
	for _, in := range []string{"", "\n", "Welcome to Ubuntu!\nsys.os=spoofed\n", "bash: sh: command not found\n"} {
		if _, err := Parse([]byte(in)); !errors.Is(err, ErrNoHeader) {
			t.Errorf("Parse(%q) err = %v, want ErrNoHeader", in, err)
		}
	}
}

// A newer probe must stay readable: unknown keys are ignored, not fatal.
func TestForwardCompatibility(t *testing.T) {
	in := "rove-probe 9\nsys.os=Future Linux\nsomething.invented=42\nload=1 2 3\n"
	s, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("a newer probe should still parse: %v", err)
	}
	if s.OS != "Future Linux" || !s.HasLoad {
		t.Errorf("snapshot = %+v", s)
	}
}

func TestOlderContractIsRejected(t *testing.T) {
	if _, err := Parse([]byte("rove-probe 0\nsys.os=x\n")); err == nil {
		t.Error("an older contract should be rejected, not silently misread")
	}
}

func TestCPUPercent(t *testing.T) {
	// 100 ticks elapsed, 25 of them idle.
	prev := model.CPUStat{User: 100, Idle: 900}
	cur := model.CPUStat{User: 175, Idle: 925}
	got, ok := model.CPUPercent(prev, cur)
	if !ok || got < 74.9 || got > 75.1 {
		t.Errorf("CPUPercent = %.2f (ok=%v), want 75", got, ok)
	}

	// No prior sample: the first refresh has nothing to compare against.
	if _, ok := model.CPUPercent(model.CPUStat{}, cur); ok {
		t.Error("a missing previous sample must report false, not 0%")
	}
	// Counters going backwards means the host rebooted between samples.
	if _, ok := model.CPUPercent(cur, prev); ok {
		t.Error("a reboot between samples must report false")
	}
}

func TestParseFSHandlesSpacesInNames(t *testing.T) {
	// macOS prints the automounter as "map auto_home", which shifts every
	// later column; the mount point may itself contain spaces.
	in := "rove-probe 1\nfs=map_auto_home 0 0 /System/Volumes/Data/home\nfs=/dev/disk1 100 50 /Volumes/My Backup Disk\n"
	s, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Filesystems) != 2 {
		t.Fatalf("got %d filesystems", len(s.Filesystems))
	}
	if s.Filesystems[1].Mount != "/Volumes/My Backup Disk" {
		t.Errorf("mount = %q", s.Filesystems[1].Mount)
	}
	if s.Filesystems[1].UsedPercent() != 50 {
		t.Errorf("used = %.0f%%", s.Filesystems[1].UsedPercent())
	}
	// A zero-size automount is not a disk anyone manages.
	if len(s.RealFilesystems()) != 1 {
		t.Errorf("real = %+v", s.RealFilesystems())
	}
}

func TestMalformedLinesAreSkippedNotFatal(t *testing.T) {
	in := "rove-probe 1\nnot a key value line\nfs=broken\nload=nonsense\nsys.os=Still Here\n"
	s, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if s.OS != "Still Here" {
		t.Errorf("OS = %q", s.OS)
	}
	if len(s.Filesystems) != 0 || s.HasLoad {
		t.Error("malformed values should be dropped, not half-parsed")
	}
}
