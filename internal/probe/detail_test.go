package probe

import (
	"errors"
	"os"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Skipf("fixture %s missing", name)
	}
	return raw
}

// procps can sort on the host, so a truncated list still holds the busiest
// processes.
func TestParseProcessesProcps(t *testing.T) {
	p, err := ParseProcesses(fixture(t, "processes/procps.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !p.SortedRemotely {
		t.Error("procps reports proc.sorted=cpu")
	}
	if len(p.Procs) == 0 {
		t.Fatal("no processes parsed")
	}
	if p.Truncated {
		t.Errorf("a %d-process host should not be truncated", p.Total)
	}
	if p.Total != len(p.Procs) {
		t.Errorf("total = %d but %d rows parsed", p.Total, len(p.Procs))
	}

	first := p.Procs[0]
	if first.PID == 0 || first.User == "" || first.Command == "" {
		t.Errorf("row not parsed: %+v", first)
	}
	if !first.HasCPU || !first.HasMem || !first.HasRSS {
		t.Errorf("procps reports all three, got %+v", first)
	}
	// The command may contain spaces and is the last field.
	for _, pr := range p.Procs {
		if pr.PID <= 0 {
			t.Errorf("bad pid in %+v", pr)
		}
	}
}

// busybox has no percentages, and prints a header row the script filters
// out. A header counted as a process makes the total wrong and the list
// look truncated when it is complete.
func TestParseProcessesBusybox(t *testing.T) {
	p, err := ParseProcesses(fixture(t, "processes/busybox.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if p.SortedRemotely {
		t.Error("busybox cannot sort; the client must")
	}
	if p.Truncated {
		t.Error("a short list must not be reported as truncated")
	}
	if p.Total != len(p.Procs) {
		t.Errorf("total = %d but %d rows parsed; a header row leaked", p.Total, len(p.Procs))
	}
	for _, pr := range p.Procs {
		if pr.HasCPU || pr.HasMem {
			t.Errorf("busybox has no percentages, got %+v", pr)
		}
		if pr.Command == "" || pr.User == "" {
			t.Errorf("row not parsed: %+v", pr)
		}
	}
}

func TestProcessTruncationIsReported(t *testing.T) {
	in := "rove-processes 1\nproc.fields=pid,user,cpu,mem,rss,args\nproc.total=400\n" +
		"proc=1 root 0.0 0.1 900 /sbin/init\n"
	p, err := ParseProcesses([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Truncated {
		t.Error("400 running but 1 returned is truncated")
	}
	if p.Total != 400 {
		t.Errorf("total = %d", p.Total)
	}
}

// A host with no usable ps says so rather than reporting an empty table.
func TestProcessErrorIsSurfaced(t *testing.T) {
	p, err := ParseProcesses([]byte("rove-processes 1\nproc.error=no usable ps on this host\n"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Err == "" {
		t.Error("the reason should survive parsing")
	}
	if len(p.Procs) != 0 {
		t.Error("no rows expected")
	}
}

func TestClientSortsWhenHostCannot(t *testing.T) {
	in := "rove-processes 1\nproc.fields=pid,user,cpu,mem,rss,args\n" +
		"proc=1 root 3.0 0.1 900 low\nproc=2 root 90.0 0.1 900 high\nproc=3 root 40.0 0.1 900 mid\n"
	p, _ := ParseProcesses([]byte(in))
	if len(p.Procs) != 3 {
		t.Fatalf("got %d rows", len(p.Procs))
	}
	if p.Procs[0].Command != "high" || p.Procs[2].Command != "low" {
		t.Errorf("unsorted: %v", []string{p.Procs[0].Command, p.Procs[1].Command, p.Procs[2].Command})
	}
}

// Captured from a real systemd host, including a unit that always fails.
func TestParseServicesSystemd(t *testing.T) {
	s, err := ParseServices(fixture(t, "services/systemd.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Supported() {
		t.Fatalf("init = %q should be supported", s.Init)
	}
	if s.Init != "systemd" {
		t.Errorf("init = %q", s.Init)
	}
	if s.State == "" {
		t.Error("is-system-running should be reported")
	}
	if len(s.Units) < 10 {
		t.Fatalf("only %d units parsed", len(s.Units))
	}

	failed := s.FailedUnits()
	if len(failed) == 0 {
		t.Fatal("the fixture has a deliberately failing unit")
	}
	var found bool
	for _, u := range failed {
		if u.Name == "rove-broken.service" {
			found = true
			if u.Active != "failed" || u.Sub != "failed" {
				t.Errorf("broken unit = %+v", u)
			}
			if u.Description == "" {
				t.Error("description should survive the split")
			}
			if u.ShortName() != "rove-broken" {
				t.Errorf("short name = %q", u.ShortName())
			}
		}
	}
	if !found {
		t.Errorf("rove-broken.service not among %d failed units", len(failed))
	}

	// A description containing spaces must not spill into other columns.
	for _, u := range s.Units {
		if u.Name == "" || u.Active == "" || u.Sub == "" {
			t.Errorf("column shift: %+v", u)
		}
	}
}

// An init system rove cannot read is reported as such. An empty unit list
// would claim the host runs no services, which is a different statement.
func TestParseServicesUnsupported(t *testing.T) {
	s, err := ParseServices(fixture(t, "services/unsupported.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Supported() {
		t.Errorf("init = %q should not be reported as supported", s.Init)
	}
	if len(s.Units) != 0 {
		t.Error("no units expected")
	}
}

func TestParseServicesHandlesUnresolvableUnit(t *testing.T) {
	in := "rove-services 1\nsvc.init=systemd\nsvc.query=ok\n" +
		"unit=ghost.service not-found inactive dead ghost.service\n"
	s, _ := ParseServices([]byte(in))
	if len(s.Units) != 1 {
		t.Fatalf("got %d units", len(s.Units))
	}
	if !s.Units[0].Missing() {
		t.Error("a not-found unit should be flagged as missing")
	}
}

// Neither detail contract may accept output that did not come from us.
func TestDetailContractsRequireTheirHeader(t *testing.T) {
	for _, in := range []string{"", "\n", "Welcome to Ubuntu\n", "rove-probe 1\nsys.os=x\n"} {
		if _, err := ParseProcesses([]byte(in)); !errors.Is(err, ErrNoHeader) {
			t.Errorf("ParseProcesses(%q) err = %v", in, err)
		}
		if _, err := ParseServices([]byte(in)); !errors.Is(err, ErrNoHeader) {
			t.Errorf("ParseServices(%q) err = %v", in, err)
		}
	}
}

// The fleet probe must distinguish "no failed units" from "could not ask".
func TestUnreadableInitIsNotSilentlyHealthy(t *testing.T) {
	readable, _ := Parse([]byte("rove-probe 1\nsvc.init=systemd\nsvc.query=ok\n"))
	if !readable.ServicesReadable() {
		t.Error("a queryable systemd is readable")
	}

	broken, _ := Parse([]byte("rove-probe 1\nsvc.init=systemd\nsvc.query=error\n"))
	if broken.ServicesReadable() {
		t.Error("systemd that could not be queried must not read as healthy")
	}
	if len(broken.FailedUnits) != 0 {
		t.Error("no failures were reported, which is the point: absence is not evidence")
	}
}
