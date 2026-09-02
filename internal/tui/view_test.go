package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	rexec "github.com/cuonggt/rove/internal/exec"
	"github.com/cuonggt/rove/internal/fleet"
	"github.com/cuonggt/rove/internal/model"
)

func healthyProbe(load string) string {
	return "rove-probe 1\n" +
		"sys.kind=linux\nsys.os=Ubuntu 24.04 LTS\nsys.kernel=6.8.0\nsys.arch=aarch64\n" +
		"sys.uptime_s=4147200\ncpu.cores=4\n" +
		"cpu.stat=cpu  1000 0 0 9000 0 0 0 0\n" +
		"load=" + load + "\n" +
		"mem.total_kb=8000000\nmem.available_kb=2640000\n" +
		"fs=/dev/sda1 80000000 32000000 /\n" +
		"net=eth0 10.0.1.24\nsvc.init=systemd\n"
}

func servers(names ...string) []model.Server {
	out := make([]model.Server, len(names))
	for i, n := range names {
		out[i] = model.Server{
			Name: n, Source: model.SourceSSHConfig, Conn: model.ConnSSH,
			Meta: model.Meta{Env: "prod", Tags: []string{"web"}},
		}
	}
	return out
}

// build returns a model whose hosts have all been probed once, sized to
// width columns.
func build(t *testing.T, fake *rexec.Fake, width int, names ...string) Model {
	t.Helper()
	f := fleet.New(fake, servers(names...), 4, time.Second)
	f.RefreshAll(context.Background())

	m := New(f, Options{Interval: time.Hour})
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	return next.(Model)
}

func healthy(t *testing.T, width int, names ...string) Model {
	t.Helper()
	return build(t, &rexec.Fake{Default: rexec.FakeResponse{Stdout: healthyProbe("0.42 0.30 0.20")}}, width, names...)
}

// Columns are dropped in a deliberate order as the terminal narrows: the
// note survives longer than the environment, because a sentence saying what
// is wrong beats a label saying where the host lives.
func TestColumnsDropInPriorityOrder(t *testing.T) {
	cases := []struct {
		width int
		want  []string
		gone  []string
	}{
		{120, []string{"HOST", "ENV", "CPU", "MEM", "DISK", "LOAD", "NOTE"}, nil},
		{90, []string{"HOST", "CPU", "MEM", "DISK", "LOAD", "NOTE"}, []string{"ENV"}},
		{80, []string{"HOST", "CPU", "MEM", "DISK"}, []string{"ENV"}},
		{64, []string{"HOST", "CPU", "MEM", "DISK"}, []string{"ENV", "NOTE"}},
	}
	for _, c := range cases {
		m := healthy(t, c.width, "web-01")
		got := m.View()
		for _, col := range c.want {
			if !strings.Contains(got, col) {
				t.Errorf("width %d: missing column %q\n%s", c.width, col, got)
			}
		}
		for _, col := range c.gone {
			if strings.Contains(got, col) {
				t.Errorf("width %d: column %q should have been dropped\n%s", c.width, col, got)
			}
		}
	}
}

// No row may push the body into wrapping; every line must fit the terminal.
func TestNoLineExceedsTerminalWidth(t *testing.T) {
	for _, width := range []int{64, 80, 100, 120, 200} {
		m := healthy(t, width, "web-01", "a-very-long-hostname-that-would-overflow", "db-01")
		for i, line := range strings.Split(m.View(), "\n") {
			if w := len([]rune(line)); w > width {
				t.Errorf("width %d: line %d is %d columns\n%q", width, i, w, line)
			}
		}
	}
}

func TestHealthyHostIsQuiet(t *testing.T) {
	m := healthy(t, 120, "web-01")
	got := m.View()
	if !strings.Contains(got, "1 host · 1 ok") {
		t.Errorf("summary missing:\n%s", got)
	}
	if strings.Contains(got, "need attention") {
		t.Errorf("a healthy fleet should not mention attention:\n%s", got)
	}
}

// A failure has to say what happened and, in the detail view, what fixes it.
func TestFailureShowsReasonAndFix(t *testing.T) {
	m := build(t, &rexec.Fake{Default: rexec.FakeResponse{
		Err: &rexec.TransportError{
			Err:    errors.New("exit status 255"),
			Stderr: "tester@web-01: Permission denied (publickey).",
		},
	}}, 120, "web-01")

	got := m.View()
	if !strings.Contains(got, "authentication failed") {
		t.Errorf("fleet view should name the failure:\n%s", got)
	}
	if !strings.Contains(got, "need attention") {
		t.Errorf("summary should count it:\n%s", got)
	}

	m.view = screenOverview
	detail := m.View()
	if !strings.Contains(detail, "ssh web-01") {
		t.Errorf("detail view should offer the fix:\n%s", detail)
	}
}

// Figures from a host that has stopped answering are kept, not blanked, and
// labelled with their age.
func TestStaleFiguresAreKeptAndAged(t *testing.T) {
	fake := &rexec.Fake{Default: rexec.FakeResponse{Stdout: healthyProbe("0.42 0.30 0.20")}}
	m := healthy(t, 120, "web-01")

	fake.Default = rexec.FakeResponse{
		Err: &rexec.TransportError{
			Err:    errors.New("exit status 255"),
			Stderr: "ssh: connect to host web-01 port 22: Operation timed out",
		},
	}
	f := fleet.New(fake, servers("web-01"), 4, time.Second)
	f.RefreshAll(context.Background()) // fails with no prior success

	// Now the case that matters: success, then failure.
	fake.Default = rexec.FakeResponse{Stdout: healthyProbe("0.42 0.30 0.20")}
	f.RefreshAll(context.Background())
	fake.Default = rexec.FakeResponse{
		Err: &rexec.TransportError{
			Err:    errors.New("exit status 255"),
			Stderr: "ssh: connect to host web-01 port 22: Operation timed out",
		},
	}
	f.RefreshAll(context.Background())

	m2 := New(f, Options{Interval: time.Hour})
	next, _ := m2.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	got := next.(Model).View()

	if !strings.Contains(got, "67%") {
		t.Errorf("memory from the last good probe should still be shown:\n%s", got)
	}
	if !strings.Contains(got, "timed out") || !strings.Contains(got, "ago") {
		t.Errorf("note should carry the reason and an age:\n%s", got)
	}
	_ = m
}

func TestFilterNarrowsRows(t *testing.T) {
	m := healthy(t, 120, "prod-api", "prod-db", "staging-api")

	m.filter = "db"
	got := m.View()
	if !strings.Contains(got, "prod-db") {
		t.Errorf("filter dropped the match:\n%s", got)
	}
	if strings.Contains(got, "staging-api") {
		t.Errorf("filter kept a non-match:\n%s", got)
	}

	m.filter = "nothing-matches-this"
	if !strings.Contains(m.View(), "no host matches") {
		t.Errorf("an empty result should say so:\n%s", m.View())
	}
}

// Tags and environment are searchable, because they are what a person types
// when they mean "the web boxes".
func TestFilterSearchesTagsAndEnv(t *testing.T) {
	m := healthy(t, 120, "alpha", "beta")
	for _, q := range []string{"prod", "web"} {
		m.filter = q
		if len(m.visible()) != 2 {
			t.Errorf("filter %q matched %d hosts, want 2", q, len(m.visible()))
		}
	}
}

// The selection follows the host, not the row. Filtering changes which index
// a host sits at; a position-based cursor would jump to a different machine.
func TestCursorSticksToHostNotIndex(t *testing.T) {
	m := healthy(t, 120, "aaa", "bbb", "ccc")

	m.cursor = "ccc"
	m.filter = "c" // ccc is now at index 0 instead of 2
	if h, ok := m.selected(); !ok || h.Server.Name != "ccc" {
		t.Fatalf("selection = %+v, want ccc", h.Server.Name)
	}

	// Filtering the selection away moves to a row that exists.
	m.filter = "a"
	m.clampCursor()
	if h, _ := m.selected(); h.Server.Name != "aaa" {
		t.Errorf("selection = %q, want aaa after its row was filtered out", h.Server.Name)
	}
}

func TestDetailViewShowsMetersAndIdentity(t *testing.T) {
	m := healthy(t, 120, "web-01")
	m.view = screenOverview
	got := m.View()

	for _, want := range []string{
		"Ubuntu 24.04 LTS", // identity
		"up 48d",           // uptime, formatted not raw seconds
		"aarch64",
		"Memory", "Disk", "Load",
		"4 cores",
		"eth0", "10.0.1.24",
		"needs a second sample", // CPU has no delta yet, and says why
	} {
		if !strings.Contains(got, want) {
			t.Errorf("detail view missing %q:\n%s", want, got)
		}
	}
}

func TestFailedUnitsAreListed(t *testing.T) {
	probe := healthyProbe("0.42 0.30 0.20") + "svc.failed=backup.service\nsvc.failed=cron.service\n"
	m := build(t, &rexec.Fake{Default: rexec.FakeResponse{Stdout: probe}}, 120, "web-01")

	if !strings.Contains(m.View(), "2 failed units") {
		t.Errorf("fleet note should count them:\n%s", m.View())
	}
	m.view = screenOverview
	got := m.View()
	if !strings.Contains(got, "backup.service") || !strings.Contains(got, "cron.service") {
		t.Errorf("detail view should name them:\n%s", got)
	}
}

// ASCII mode must not emit any character outside the printable ASCII range,
// so the interface survives a terminal without a Unicode font.
func TestASCIIModeEmitsOnlyASCII(t *testing.T) {
	f := fleet.New(&rexec.Fake{Default: rexec.FakeResponse{Stdout: healthyProbe("9.2 8 7")}},
		servers("web-01"), 4, time.Second)
	f.RefreshAll(context.Background())

	m := New(f, Options{Interval: time.Hour, ASCII: true})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	mm := next.(Model)

	for _, view := range []string{mm.View(), func() string { mm.view = screenOverview; return mm.View() }()} {
		for _, r := range view {
			if r > 126 {
				t.Fatalf("non-ascii rune %q in ascii mode:\n%s", r, view)
			}
		}
	}
}

// A refresh must not be re-queued on every frame once the interval elapses.
func TestRefreshIsNotRequeuedEveryTick(t *testing.T) {
	f := fleet.New(&rexec.Fake{Default: rexec.FakeResponse{Stdout: healthyProbe("0.42 0.30 0.20")}},
		servers("web-01"), 4, time.Second)

	m := New(f, Options{Interval: time.Millisecond})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	mm := next.(Model)
	mm.inflight = map[string]bool{} // pretend the opening round finished

	time.Sleep(2 * time.Millisecond)

	n1, _ := mm.Update(tickMsg(time.Now()))
	first := n1.(Model)
	if first.lastRefresh.Equal(mm.lastRefresh) {
		t.Fatal("an elapsed interval should stamp lastRefresh")
	}

	// Immediately after, with the interval not yet elapsed again, nothing
	// new should be scheduled.
	first.inflight = map[string]bool{}
	n2, _ := first.Update(tickMsg(time.Now()))
	if !n2.(Model).lastRefresh.Equal(first.lastRefresh) {
		t.Error("a refresh was re-queued before the interval elapsed")
	}
}

func TestNarrowTerminalSaysSoInsteadOfBreaking(t *testing.T) {
	m := healthy(t, 40, "web-01")
	if !strings.Contains(m.View(), "wider terminal") {
		t.Errorf("a too-narrow terminal should explain itself:\n%s", m.View())
	}
}

// The logs screen exists to answer "failed how". These assert the reader is
// never shown a partial or absent log as if it were the whole story.
func TestLogsViewSaysWhyItIsEmpty(t *testing.T) {
	m := healthy(t, 120, "web-01")
	m.view = screenLogs
	m.logUnit = "backup.service"
	m.logs[logKey("web-01", "backup.service")] = model.LogTail{
		Source: "none",
		Err:    "this account sees only its own messages; add it to the systemd-journal or adm group",
	}

	out := m.View()
	if !strings.Contains(out, "no log to read") {
		t.Error("an unreadable log must say so")
	}
	if !strings.Contains(out, "systemd-journal") {
		t.Error("the reason must name the fix")
	}
}

func TestLogsViewFlagsAPartialTail(t *testing.T) {
	m := healthy(t, 120, "web-01")
	m.view = screenLogs
	m.logs[logKey("web-01", "")] = model.LogTail{
		Source:  "journald",
		Limited: true,
		Lines:   []string{"2026-09-02 something happened"},
	}

	out := m.View()
	if !strings.Contains(out, "only this account's own messages") {
		t.Error("a partial tail must be flagged above the lines")
	}
}

// A syslog file filters by substring, not by unit, so the footer must not
// imply journald accuracy.
func TestLogsViewNamesANonJournalSource(t *testing.T) {
	m := healthy(t, 120, "web-01")
	m.view = screenLogs
	m.logs[logKey("web-01", "")] = model.LogTail{
		Source: "/var/log/messages",
		Lines:  []string{"line one"},
	}

	out := m.View()
	if !strings.Contains(out, "file /var/log/messages") {
		t.Errorf("footer should name the file source, got:\n%s", out)
	}
}

// The screen switcher grows with every screen. It must give way to the
// status rather than pushing it off the line, at every width.
func TestFooterKeepsStatusAsScreensAccumulate(t *testing.T) {
	for _, width := range []int{80, 100, 120, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := healthy(t, width, "web-01")
			m.view = screenPorts
			m.ports["web-01"] = model.PortList{
				Source:    "ss",
				Listeners: []model.Listener{{Proto: "tcp", Addr: "0.0.0.0", Port: 22, Process: "sshd", HasProcess: true}},
				Conns:     []model.ConnState{{State: "ESTAB", Count: 3}},
			}

			out := m.View()
			if !strings.Contains(out, "1 listening") {
				t.Errorf("status lost at width %d:\n%s", width, out)
			}
			for _, line := range strings.Split(out, "\n") {
				if lipglossWidth(line) > width {
					t.Errorf("line overflows %d columns: %q", width, line)
				}
			}
		})
	}
}

// The judgment the raw tools do not make: what the network can reach.
func TestPortsViewMarksNetworkFacingSockets(t *testing.T) {
	m := healthy(t, 120, "web-01")
	m.view = screenPorts
	m.ports["web-01"] = model.PortList{
		Source: "ss",
		Listeners: []model.Listener{
			{Proto: "tcp", Addr: "127.0.0.1", Port: 5432, Process: "postgres", HasProcess: true},
			{Proto: "tcp", Addr: "0.0.0.0", Port: 443, Process: "nginx", HasProcess: true},
		},
	}

	out := m.View()
	if !strings.Contains(out, "network") || !strings.Contains(out, "local") {
		t.Error("scope must be stated, not inferred from the address")
	}
	if !strings.Contains(out, "1 on the network") {
		t.Error("the footer should count what faces the network")
	}
	// 443 is exposed and must sort above the loopback 5432.
	if strings.Index(out, "443") > strings.Index(out, "5432") {
		t.Error("network-facing sockets must appear first")
	}
}

// Missing owners mean "root required", not "no process".
func TestPortsViewExplainsMissingOwners(t *testing.T) {
	m := healthy(t, 120, "web-01")
	m.view = screenPorts
	m.ports["web-01"] = model.PortList{
		Source:    "ss",
		Limited:   true,
		Listeners: []model.Listener{{Proto: "tcp", Addr: "0.0.0.0", Port: 22}},
	}

	out := m.View()
	if !strings.Contains(out, "owners need root") {
		t.Error("a blank owner column must be explained")
	}
	if !strings.Contains(out, "ports below are complete") {
		t.Error("the reader must know the ports themselves are not truncated")
	}
}

// lipglossWidth measures display cells, so styled output is compared by what
// the terminal shows rather than by byte length.
func lipglossWidth(s string) int { return lipgloss.Width(s) }

// Detection has three answers, and collapsing them into an empty table
// would tell the reader the wrong thing three different ways.
func TestContainersViewSeparatesTheThreeAnswers(t *testing.T) {
	cases := []struct {
		name   string
		list   model.ContainerList
		expect string
		absent string
	}{
		{
			name:   "no runtime at all",
			list:   model.ContainerList{Source: "none", Err: "no container runtime found on this host"},
			expect: "no container runtime on this host",
		},
		{
			name:   "installed but unusable",
			list:   model.ContainerList{CLI: "docker", Source: "none", Err: "docker is installed but its daemon is not reachable"},
			expect: "docker is installed but unusable",
			absent: "no container runtime on this host",
		},
		{
			name:   "running with nothing on it",
			list:   model.ContainerList{CLI: "docker", Source: "docker", Version: "29.4.0"},
			expect: "with no containers",
			absent: "unusable",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := healthy(t, 120, "web-01")
			m.view = screenContainers
			m.conts["web-01"] = c.list

			out := m.View()
			if !strings.Contains(out, c.expect) {
				t.Errorf("missing %q in:\n%s", c.expect, out)
			}
			if c.absent != "" && strings.Contains(out, c.absent) {
				t.Errorf("should not say %q", c.absent)
			}
		})
	}
}

func TestContainersViewFlagsPublishedPorts(t *testing.T) {
	m := healthy(t, 140, "web-01")
	m.view = screenContainers
	m.conts["web-01"] = model.ContainerList{
		CLI: "docker", Source: "docker",
		Containers: []model.Container{
			{ID: "a", State: "running", Name: "web", Image: "nginx", Status: "Up 2h", Ports: "0.0.0.0:443->443/tcp"},
			{ID: "b", State: "exited", Name: "job", Image: "busybox", Status: "Exited (1) 3m ago"},
		},
	}

	out := m.View()
	if !strings.Contains(out, "1 running") || !strings.Contains(out, "1 stopped") {
		t.Error("the footer should count both")
	}
	if !strings.Contains(out, "1 publishing") {
		t.Error("a published port bypasses host firewall rules and should be counted")
	}
	// A stopped container is usually why this screen was opened.
	if strings.Index(out, "web") > strings.Index(out, "job") {
		t.Error("running containers sort above stopped ones")
	}
}

func duModel(t *testing.T, usage model.DirUsage) Model {
	t.Helper()
	m := healthy(t, 120, "web-01")
	m.view = screenDiskUsage
	m.duPath = usage.Path
	m.du[logKey("web-01", usage.Path)] = usage
	return m
}

// Every figure from an unprivileged du is a floor. Saying so after the
// numbers would be too late; it changes what they mean.
func TestDrillDownFlagsInexactFiguresBeforeTheNumbers(t *testing.T) {
	cases := []struct {
		name  string
		usage model.DirUsage
		want  string
	}{
		{"unreadable", model.DirUsage{
			Path: "/var", Unreadable: 6,
			Entries: []model.DirEntry{{Path: "/var", KB: 1000}, {Path: "/var/log", KB: 900}},
		}, "6 directories could not be read"},
		{"timed out", model.DirUsage{
			Path: "/var", TimedOut: true,
			Entries: []model.DirEntry{{Path: "/var", KB: 1000}, {Path: "/var/log", KB: 900}},
		}, "hit its time limit"},
		{"shallow", model.DirUsage{
			Path: "/var", Shallow: true,
			Entries: []model.DirEntry{{Path: "/var", KB: 1000}},
		}, "cannot descend"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := duModel(t, c.usage).View()
			if !strings.Contains(out, c.want) {
				t.Errorf("missing %q in:\n%s", c.want, out)
			}
			if !strings.Contains(out, "at least") {
				t.Error("an inexact total must be labelled as a floor")
			}
			// The caveat has to precede the table it qualifies.
			if i, j := strings.Index(out, c.want), strings.Index(out, "SIZE"); j >= 0 && i > j {
				t.Error("the caveat appears below the numbers it qualifies")
			}
		})
	}
}

func TestDrillDownExactRunSaysNothingExtra(t *testing.T) {
	out := duModel(t, model.DirUsage{
		Path:    "/var",
		Entries: []model.DirEntry{{Path: "/var", KB: 1000}, {Path: "/var/log", KB: 900}},
	}).View()

	if strings.Contains(out, "at least") || strings.Contains(out, "floor") {
		t.Error("a complete walk should not hedge")
	}
	if !strings.Contains(out, "90%") {
		t.Error("share of the parent is the number people scan for")
	}
}

// Enter descends, backspace walks back out the way it came in.
func TestDrillDownNavigation(t *testing.T) {
	m := healthy(t, 120, "web-01")
	m.view = screenDisk
	m.rowIndex = 0

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(Model)
	if got.view != screenDiskUsage {
		t.Fatalf("enter on a mount should drill in, got view %v", got.view)
	}
	if got.duPath == "" {
		t.Fatal("no path was selected to drill into")
	}
	first := got.duPath

	// Descend again into a child.
	got.du[logKey("web-01", first)] = model.DirUsage{
		Path:    first,
		Entries: []model.DirEntry{{Path: first, KB: 100}, {Path: first + "/log", KB: 90}},
	}
	got.rowIndex = 0
	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	deeper := next.(Model)
	if deeper.duPath != first+"/log" {
		t.Fatalf("duPath = %q, want %q", deeper.duPath, first+"/log")
	}

	// And back out, one level at a time.
	next, _ = deeper.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	back := next.(Model)
	if back.duPath != first {
		t.Errorf("backspace should return to %q, got %q", first, back.duPath)
	}
	next, _ = back.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if out := next.(Model); out.view != screenDisk {
		t.Errorf("backspace at the top should return to the disk list, got %v", out.view)
	}
}

// Leaving the screen must not strand a trail that later backspaces walk.
func TestEscapeClearsTheDrillTrail(t *testing.T) {
	m := healthy(t, 120, "web-01")
	m.view = screenDisk
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(Model).Update(tea.KeyMsg{Type: tea.KeyEsc})

	got := next.(Model)
	if got.view != screenFleet || len(got.duStack) != 0 || got.duPath != "" {
		t.Errorf("view=%v stack=%v path=%q", got.view, got.duStack, got.duPath)
	}
}
