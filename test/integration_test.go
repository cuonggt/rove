//go:build integration

// Integration coverage against real sshd on real distributions. Kept behind
// a build tag so `go test ./...` stays fast and offline.
//
//	test/fixtures/sshd/up.sh
//	go test -tags=integration ./test/...
//	test/fixtures/sshd/down.sh
package test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cuonggt/rove/internal/action"
	rexec "github.com/cuonggt/rove/internal/exec"
	"github.com/cuonggt/rove/internal/fleet"
	"github.com/cuonggt/rove/internal/inventory"
	"github.com/cuonggt/rove/internal/model"
)

const configPath = "fixtures/sshd/config"

func newFleet(t *testing.T) *fleet.Fleet {
	t.Helper()
	if _, err := os.Stat(configPath); err != nil {
		t.Skip("fixtures not running; start them with fixtures/sshd/up.sh")
	}
	ctx := context.Background()
	servers, err := inventory.Load(ctx, configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) == 0 {
		t.Fatal("no fixture hosts discovered")
	}
	ssh := rexec.NewSSH()
	ssh.ConfigPath = configPath
	return fleet.New(ssh, servers, 8, 15*time.Second)
}

func TestProbeEveryDistro(t *testing.T) {
	f := newFleet(t)
	f.RefreshAll(context.Background())

	for _, h := range f.Hosts() {
		t.Run(h.Server.Name, func(t *testing.T) {
			if h.Status != model.StatusOK {
				t.Fatalf("status = %q: %s", h.Status, h.Reason)
			}
			if h.Snap.OS == "" || h.Snap.Kernel == "" {
				t.Errorf("identity missing: os=%q kernel=%q", h.Snap.OS, h.Snap.Kernel)
			}
			if h.Snap.Kind != "linux" {
				t.Errorf("kind = %q", h.Snap.Kind)
			}
			if !h.Snap.HasCPU || !h.Snap.HasLoad {
				t.Error("cpu or load missing")
			}
			if pct, ok := h.Snap.MemUsedPercent(); !ok || pct <= 0 || pct >= 100 {
				t.Errorf("memory = %.1f%% (ok=%v)", pct, ok)
			}
			if _, ok := h.Snap.FullestFilesystem(); !ok {
				t.Error("no real filesystem after filtering")
			}
			// The whole point of one round trip: this is remote wall time.
			if h.Snap.ProbeMS > 400 {
				t.Errorf("probe took %dms on the host, budget is 400ms", h.Snap.ProbeMS)
			}
		})
	}
}

// The annotation lives in an ssh_config comment, which OpenSSH ignores.
func TestAnnotationsAreRead(t *testing.T) {
	ctx := context.Background()
	if _, err := os.Stat(configPath); err != nil {
		t.Skip("fixtures not running")
	}
	servers, err := inventory.Load(ctx, configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range servers {
		if s.Meta.Env != "fixture" {
			t.Errorf("%s: env = %q, want fixture", s.Name, s.Meta.Env)
		}
		if len(s.Meta.Tags) == 0 {
			t.Errorf("%s: no tags", s.Name)
		}
		if s.Address != "127.0.0.1" {
			t.Errorf("%s: address = %q; -F was not honoured", s.Name, s.Address)
		}
	}
}

// A wrong key must be reported as an auth failure that names its fix, not as
// a host that is simply down.
func TestWrongKeyIsClassifiedAsAuth(t *testing.T) {
	f := newFleet(t)
	f.RefreshAll(context.Background())
	hosts := f.Hosts()
	if len(hosts) == 0 {
		t.Skip("no hosts")
	}

	ssh := rexec.NewSSH()
	ssh.ConfigPath = configPath
	ssh.Multiplex = false // a live master would bypass authentication

	bad := hosts[0].Server
	single := fleet.New(&noKeyExecutor{ssh}, []model.Server{bad}, 1, 15*time.Second)
	single.RefreshAll(context.Background())

	h := single.Hosts()[0]
	if h.Status != model.StatusAuth {
		t.Fatalf("status = %q (%s), want auth", h.Status, h.Reason)
	}
	if !strings.Contains(h.Fix, bad.Name) {
		t.Errorf("fix = %q, should name the host", h.Fix)
	}
}

// noKeyExecutor strips the fixture key so authentication has to fail.
type noKeyExecutor struct{ inner *rexec.SSH }

func (n *noKeyExecutor) Run(ctx context.Context, t model.Target, c rexec.Command) (rexec.Result, error) {
	prev := os.Getenv("SSH_AUTH_SOCK")
	os.Unsetenv("SSH_AUTH_SOCK")
	defer os.Setenv("SSH_AUTH_SOCK", prev)

	inner := *n.inner
	inner.ConfigPath = "fixtures/sshd/config-nokey"
	return inner.Run(ctx, t, c)
}

// The v0.2 promise, end to end: a unit is marked failed on the fleet view,
// and the log says why without anyone recalling journalctl's flags.
func TestLogsExplainAFailedUnit(t *testing.T) {
	f := newFleet(t)
	const host = "rove-fixture-ubuntu-systemd"
	if _, ok := f.Server(host); !ok {
		t.Skip("systemd fixture not running")
	}
	ctx := context.Background()

	svcs, err := f.Services(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	failed := svcs.FailedUnits()
	if len(failed) == 0 {
		t.Fatal("the fixture ships a unit that always fails; none was reported")
	}

	tail, err := f.Logs(ctx, host, failed[0].Name, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !tail.Available() {
		t.Fatalf("no log for %s: %s", failed[0].Name, tail.Err)
	}
	if tail.Partial() {
		t.Errorf("the fixture account is in adm and should see the whole journal")
	}

	joined := strings.Join(tail.Lines, "\n")
	if !strings.Contains(joined, "Failed with result") {
		t.Errorf("the log does not explain the failure:\n%s", joined)
	}
}

// Every host must answer a system-wide log request with either lines or a
// reason. Silence would be indistinguishable from a healthy quiet host.
func TestEveryHostExplainsItsLogSituation(t *testing.T) {
	f := newFleet(t)
	ctx := context.Background()
	for _, h := range f.Hosts() {
		t.Run(h.Server.Name, func(t *testing.T) {
			tail, err := f.Logs(ctx, h.Server.Name, "", 20)
			if err != nil {
				t.Fatal(err)
			}
			if !tail.Available() && tail.Err == "" {
				t.Error("an unavailable log must carry a reason")
			}
			if tail.Available() && len(tail.Lines) == 0 && tail.Err == "" {
				t.Error("an empty tail must say why it is empty")
			}
		})
	}
}

// A unit name reaches a remote shell through ssh's argv concatenation, and
// it originates from that host's own output.
func TestHostileUnitNameNeverReachesTheHost(t *testing.T) {
	f := newFleet(t)
	hosts := f.Hosts()
	if len(hosts) == 0 {
		t.Skip("no hosts")
	}
	_, err := f.Logs(context.Background(), hosts[0].Server.Name, "x; id > /tmp/rove-pwned", 10)
	if err == nil {
		t.Fatal("a shell-injecting unit name was accepted")
	}
}

// Ports, end to end. Every fixture runs sshd, so every one of them must
// report something listening on 22 and say whether owners were readable.
func TestPortsAcrossDistros(t *testing.T) {
	f := newFleet(t)
	ctx := context.Background()

	for _, h := range f.Hosts() {
		t.Run(h.Server.Name, func(t *testing.T) {
			list, err := f.Ports(ctx, h.Server.Name)
			if err != nil {
				t.Fatal(err)
			}
			if !list.Available() {
				t.Fatalf("no socket source: %s", list.Err)
			}

			var ssh bool
			for _, l := range list.Listeners {
				if l.Port == 22 {
					ssh = true
				}
				if l.Port <= 0 || l.Port > 65535 {
					t.Errorf("implausible port %d", l.Port)
				}
			}
			if !ssh {
				t.Error("we arrived over ssh; port 22 should be listening")
			}
			if list.ExposedCount() == 0 {
				t.Error("sshd binds the network, so something should be exposed")
			}

			// An unprivileged account cannot read another user's socket
			// owner. Whichever it was, the capture must say so rather than
			// leaving a blank column to be misread.
			if list.Limited {
				for _, l := range list.Listeners {
					if l.HasProcess {
						t.Errorf("limited listing named an owner: %+v", l)
					}
				}
			}
		})
	}
}

// None of the fixtures has a container runtime, which is itself the case
// worth asserting: absence must be reported as absence, not as an empty
// list that reads like a host with nothing running.
func TestContainerAbsenceIsReportedAsAbsence(t *testing.T) {
	f := newFleet(t)
	ctx := context.Background()

	for _, h := range f.Hosts() {
		t.Run(h.Server.Name, func(t *testing.T) {
			list, err := f.Containers(ctx, h.Server.Name)
			if err != nil {
				t.Fatal(err)
			}
			if list.Available() {
				// If a fixture ever gains a runtime, the listing must still
				// be coherent rather than half-parsed.
				for _, c := range list.Containers {
					if c.ID == "" || c.Name == "" {
						t.Errorf("incomplete container: %+v", c)
					}
				}
				return
			}
			if list.Err == "" {
				t.Error("an unavailable runtime must say why")
			}
			if list.Installed() {
				t.Errorf("no fixture installs a runtime, but %q was found", list.CLI)
			}
		})
	}
}

// The fleet probe detects a runtime cheaply so that a container host can be
// recognised without listing anything.
func TestFleetProbeReportsNoRuntimeOnFixtures(t *testing.T) {
	f := newFleet(t)
	f.RefreshAll(context.Background())
	for _, h := range f.Hosts() {
		if h.Snap.ContainerRuntime != "" {
			t.Errorf("%s: unexpected runtime %q", h.Server.Name, h.Snap.ContainerRuntime)
		}
	}
}

// Drill-down, end to end: the question behind "/var is 91% full".
func TestDrillDownFindsWhatIsFillingAPath(t *testing.T) {
	f := newFleet(t)
	ctx := context.Background()
	hosts := f.Hosts()
	if len(hosts) == 0 {
		t.Skip("no hosts")
	}
	host := hosts[0].Server.Name

	usage, err := f.DiskUsage(ctx, host, "/var")
	if err != nil {
		t.Fatal(err)
	}
	if usage.Err != "" {
		t.Fatalf("du failed: %s", usage.Err)
	}
	if usage.Total() <= 0 {
		t.Fatal("no total for /var")
	}
	if usage.Path != "/var" {
		t.Errorf("path = %q", usage.Path)
	}

	kids := usage.Children()
	if len(kids) == 0 {
		t.Fatal("/var has subdirectories; none were reported")
	}
	for i := 1; i < len(kids); i++ {
		if kids[i-1].KB < kids[i].KB {
			t.Fatalf("children are not largest-first at %d", i)
		}
	}
	// Shares must be a meaningful fraction of the parent, not of nothing.
	if s := usage.Share(kids[0]); s <= 0 || s > 100 {
		t.Errorf("largest child is %.1f%% of the parent", s)
	}

	// Descending into the largest child must work the same way.
	deeper, err := f.DiskUsage(ctx, host, kids[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if deeper.Total() > usage.Total() {
		t.Errorf("child %s (%d) larger than its parent (%d)", kids[0].Path, deeper.Total(), usage.Total())
	}
}

// An unprivileged du skips what it cannot read and still prints a plausible
// total. Every fixture logs in unprivileged, so this is the common case.
func TestUnreadableDirectoriesAreCounted(t *testing.T) {
	f := newFleet(t)
	const host = "rove-fixture-ubuntu-systemd"
	if _, ok := f.Server(host); !ok {
		t.Skip("systemd fixture not running")
	}
	usage, err := f.DiskUsage(context.Background(), host, "/var")
	if err != nil {
		t.Fatal(err)
	}
	if usage.Unreadable == 0 {
		t.Skip("this account could read all of /var; nothing to assert")
	}
	if usage.Exact() {
		t.Error("a walk that skipped directories must not claim to be exact")
	}
}

// A mount point arrives from the host's own df output.
func TestHostilePathNeverReachesTheHost(t *testing.T) {
	f := newFleet(t)
	hosts := f.Hosts()
	if len(hosts) == 0 {
		t.Skip("no hosts")
	}
	if _, err := f.DiskUsage(context.Background(), hosts[0].Server.Name, "/var; id > /tmp/rove-pwned"); err == nil {
		t.Fatal("a shell-injecting path was accepted")
	}
}

// Process detail, end to end. Every fixture is itself a container, so pid 1
// exercises the cgroup path that ps cannot see.
func TestProcessDetailAcrossDistros(t *testing.T) {
	f := newFleet(t)
	ctx := context.Background()

	for _, h := range f.Hosts() {
		t.Run(h.Server.Name, func(t *testing.T) {
			d, err := f.ProcessDetail(ctx, h.Server.Name, 1)
			if err != nil {
				t.Fatal(err)
			}
			if !d.Found() {
				t.Fatalf("pid 1 must exist: %s", d.Err)
			}
			if d.Comm == "" {
				t.Error("no command name")
			}
			if d.User == "" && d.UID != 0 {
				t.Errorf("uid %d resolved to no account", d.UID)
			}
			if d.Threads <= 0 {
				t.Error("a running process has at least one thread")
			}
			if d.StateLabel() == "" {
				t.Error("no state")
			}
			// Container detection reads the cgroup path, which a process
			// in its own cgroup namespace cannot see: these fixtures read
			// their own cgroup as "0::/". So absence is correct here, and
			// what matters is that whatever is reported is coherent.
			if d.InContainer() && len(d.ShortContainer()) != 12 {
				t.Errorf("container id looks wrong: %q", d.Container)
			}
		})
	}
}

// The systemd fixture runs with the host cgroup namespace, which is what a
// real container host looks like from the outside. That is the only place
// the cgroup path exposes a container id, and it is the case rove is for.
func TestContainerMembershipIsSeenWhereTheCgroupExposesIt(t *testing.T) {
	f := newFleet(t)
	const host = "rove-fixture-ubuntu-systemd"
	if _, ok := f.Server(host); !ok {
		t.Skip("systemd fixture not running")
	}
	d, err := f.ProcessDetail(context.Background(), host, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !d.InContainer() {
		t.Fatal("this fixture shares the host cgroup namespace; its container id should be visible")
	}
	if len(d.Container) != 64 || len(d.ShortContainer()) != 12 {
		t.Errorf("container = %q", d.Container)
	}
}

// A pid that has gone is a normal answer, not a transport failure.
func TestVanishedProcessIsAnAnswer(t *testing.T) {
	f := newFleet(t)
	hosts := f.Hosts()
	if len(hosts) == 0 {
		t.Skip("no hosts")
	}
	d, err := f.ProcessDetail(context.Background(), hosts[0].Server.Name, 4194303)
	if err != nil {
		t.Fatalf("a missing pid should not error the transport: %v", err)
	}
	if d.Found() || d.Err == "" {
		t.Errorf("found=%v err=%q", d.Found(), d.Err)
	}
}

func TestInvalidPIDNeverReachesTheHost(t *testing.T) {
	f := newFleet(t)
	hosts := f.Hosts()
	if len(hosts) == 0 {
		t.Skip("no hosts")
	}
	if _, err := f.ProcessDetail(context.Background(), hosts[0].Server.Name, -1); err == nil {
		t.Fatal("a negative pid was accepted")
	}
}

// Actions, end to end. The fixture grants passwordless sudo for systemctl
// only, so this exercises the sudo path the way a real locked-down host
// would, rather than running as root and proving less.
func TestActionRestartsAUnit(t *testing.T) {
	f := newFleet(t)
	const host = "rove-fixture-ubuntu-systemd"
	if _, ok := f.Server(host); !ok {
		t.Skip("systemd fixture not running")
	}
	ctx := context.Background()

	// rove-ok exists precisely so that action tests never have to touch
	// sshd, which is the connection they are running over.
	const unit = "rove-ok.service"
	restart := action.Action{Kind: action.ServiceRestart, Target: unit}

	res, err := f.Act(ctx, host, restart, action.Confirm(host, restart))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("restart failed: %s (privilege=%s)", res.Err, res.Privilege)
	}
	if res.Privilege != "sudo" {
		t.Errorf("privilege = %q; the fixture logs in unprivileged", res.Privilege)
	}
	// An action that reports success without saying what the thing now
	// looks like leaves the reader to go and check anyway.
	if res.State != "active" {
		t.Errorf("state after restart = %q, want active", res.State)
	}
}

// Stop then start, so the dangerous path is exercised rather than assumed.
func TestActionStopsAndStartsAUnit(t *testing.T) {
	f := newFleet(t)
	const host = "rove-fixture-ubuntu-systemd"
	if _, ok := f.Server(host); !ok {
		t.Skip("systemd fixture not running")
	}
	ctx := context.Background()
	const unit = "rove-ok.service"

	stop := action.Action{Kind: action.ServiceStop, Target: unit}
	res, err := f.Act(ctx, host, stop, action.Confirm(host, stop))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.State == "active" {
		t.Fatalf("stop: ok=%v state=%q err=%q", res.OK, res.State, res.Err)
	}

	start := action.Action{Kind: action.ServiceStart, Target: unit}
	res, err = f.Act(ctx, host, start, action.Confirm(host, start))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.State != "active" {
		t.Fatalf("start: ok=%v state=%q err=%q", res.OK, res.State, res.Err)
	}
}

// The fixture's sudo rule covers systemctl and nothing else, so this is a
// genuine "not permitted" rather than a simulated one.
func TestActionWithoutPermissionSaysSo(t *testing.T) {
	f := newFleet(t)
	const host = "rove-fixture-ubuntu-systemd"
	if _, ok := f.Server(host); !ok {
		t.Skip("systemd fixture not running")
	}
	// pid 2 is a kernel thread: real, and not ours to signal.
	a := action.Action{Kind: action.ProcessTerm, Target: "2"}
	res, err := f.Act(context.Background(), host, a, action.Confirm(host, a))
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("signalling a kernel thread as an unprivileged user should not succeed")
	}
	if res.Err == "" {
		t.Error("a refused action must say why")
	}
}

// Nothing reaches a host without a confirmation naming that exact host.
func TestUnconfirmedActionNeverReachesAHost(t *testing.T) {
	f := newFleet(t)
	hosts := f.Hosts()
	if len(hosts) == 0 {
		t.Skip("no hosts")
	}
	host := hosts[0].Server.Name
	a := action.Action{Kind: action.ServiceRestart, Target: "rove-ok.service"}

	if _, err := f.Act(context.Background(), host, a, action.Confirmation{}); !errors.Is(err, action.ErrNotConfirmed) {
		t.Fatalf("err = %v, want ErrNotConfirmed", err)
	}
	// A confirmation for another machine is not agreement for this one.
	if _, err := f.Act(context.Background(), host, a, action.Confirm("somewhere-else", a)); !errors.Is(err, action.ErrNotConfirmed) {
		t.Fatalf("err = %v, want ErrNotConfirmed", err)
	}
}
