package probe

import (
	"os"
	"strings"
	"testing"
)

// Captured from a real daemon: sixteen containers with published ports.
func TestGoldenDockerRunning(t *testing.T) {
	raw, err := os.ReadFile("testdata/docker/running.txt")
	if err != nil {
		t.Skip("fixture missing")
	}
	l, err := ParseDocker(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Available() || l.Source != "docker" {
		t.Fatalf("source = %q", l.Source)
	}
	if l.Version == "" {
		t.Error("a reachable daemon reports its version")
	}
	if len(l.Containers) == 0 {
		t.Fatal("no containers parsed")
	}

	for _, c := range l.Containers {
		if c.ID == "" || c.Name == "" || c.Image == "" {
			t.Errorf("incomplete container: %+v", c)
		}
		if len(c.ShortID()) != 12 {
			t.Errorf("short id = %q", c.ShortID())
		}
	}
	if l.RunningCount() == 0 {
		t.Error("the fixtures were up when this was captured")
	}
	// The fixture containers publish ssh on every interface.
	if l.ExposedCount() == 0 {
		t.Error("published ports should be recognised as network-facing")
	}
}

// "docker is installed but its daemon is not reachable" is a different and
// more useful statement than "no docker on this host".
func TestGoldenDockerDaemonDown(t *testing.T) {
	raw, err := os.ReadFile("testdata/docker/daemon-down.txt")
	if err != nil {
		t.Skip("fixture missing")
	}
	l, err := ParseDocker(raw)
	if err != nil {
		t.Fatal(err)
	}
	if l.Available() {
		t.Error("an unreachable daemon is not available")
	}
	if !l.Installed() || l.CLI != "docker" {
		t.Error("the binary was found; that must be reported separately")
	}
	if !strings.Contains(l.Err, "not reachable") {
		t.Errorf("err = %q", l.Err)
	}
	if len(l.Err) > 160 {
		t.Errorf("error is a paragraph, not a cell: %d chars", len(l.Err))
	}
}

func TestGoldenDockerAbsent(t *testing.T) {
	raw, err := os.ReadFile("testdata/docker/absent.txt")
	if err != nil {
		t.Skip("fixture missing")
	}
	l, err := ParseDocker(raw)
	if err != nil {
		t.Fatal(err)
	}
	if l.Available() || l.Installed() {
		t.Errorf("cli=%q source=%q", l.CLI, l.Source)
	}
	if l.Err == "" {
		t.Error("absence must still be explained")
	}
}

// Tabs exist so that spaces inside a field cannot shift the columns.
func TestFieldsWithSpacesSurvive(t *testing.T) {
	in := "rove-docker 1\ndocker.source=docker\n" +
		"container=abc123def4567\trunning\tmy app\tregistry.io/team/my app:1.2\tUp 2 hours (healthy)\t0.0.0.0:443->443/tcp, :::443->443/tcp\n"
	l, err := ParseDocker([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	c := l.Containers[0]
	if c.Name != "my app" || c.Image != "registry.io/team/my app:1.2" {
		t.Errorf("name=%q image=%q", c.Name, c.Image)
	}
	if c.Status != "Up 2 hours (healthy)" {
		t.Errorf("status = %q", c.Status)
	}
	if !c.Exposed() {
		t.Error("0.0.0.0 publish should read as network-facing")
	}
}

// A runtime with no State field still says what it is in the status text.
func TestStateDerivedFromStatusOnOlderRuntimes(t *testing.T) {
	in := "rove-docker 1\ndocker.source=docker\n" +
		"container=a1\t\tweb\tnginx\tUp 3 days\t\n" +
		"container=b2\t\tjob\tbusybox\tExited (1) 5 minutes ago\t\n"
	l, _ := ParseDocker([]byte(in))
	if !l.Containers[0].Running() {
		t.Error("Up should mean running")
	}
	if l.Containers[1].State != "exited" {
		t.Errorf("state = %q", l.Containers[1].State)
	}
	// A stopped container is usually the reason for opening this screen.
	if got := l.Ordered(); got[0].State != "running" || got[1].State != "exited" {
		t.Errorf("order = %+v", got)
	}
}
