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
	"os"
	"strings"
	"testing"
	"time"

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
