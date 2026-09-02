//go:build integration

package test

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	rexec "github.com/cuonggt/rove/internal/exec"
	"github.com/cuonggt/rove/internal/fleet"
	"github.com/cuonggt/rove/internal/inventory"
	"github.com/cuonggt/rove/internal/tui"
)

// TestRenderFrames drives the interface against the live fixtures and logs
// what it draws. It asserts the frames are well formed, and `go test -v`
// prints them, which is the only way to eyeball the interface from a
// terminal that cannot allocate a pty.
func TestRenderFrames(t *testing.T) {
	f := newFleet(t)
	f.RefreshAll(context.Background())
	// A second pass so the CPU delta has something to work with.
	f.RefreshAll(context.Background())

	m := tui.New(f, tui.Options{Interval: time.Hour, ConfigPath: configPath})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})

	fleetFrame := next.(tui.Model).View()
	t.Logf("\n%s", fleetFrame)

	if !strings.Contains(fleetFrame, "hosts") || !strings.Contains(fleetFrame, "DISK") {
		t.Error("fleet frame is missing its summary or columns")
	}
	for i, line := range strings.Split(fleetFrame, "\n") {
		if w := len([]rune(line)); w > 110 {
			t.Errorf("line %d overflows the terminal at %d columns", i, w)
		}
	}

	// Enter opens the overview card for the selected host.
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	detailFrame := next.(tui.Model).View()
	t.Logf("\n%s", detailFrame)

	for _, want := range []string{"Memory", "Disk", "Load", "init"} {
		if !strings.Contains(detailFrame, want) {
			t.Errorf("detail frame missing %q", want)
		}
	}

	// Esc returns to the fleet.
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if back := next.(tui.Model).View(); !strings.Contains(back, "DISK") {
		t.Error("esc did not return to the fleet view")
	}
}

// The filter is typed, not configured. This walks the real key path.
func TestFilterByTyping(t *testing.T) {
	f := newFleet(t)
	f.RefreshAll(context.Background())

	m := tui.New(f, tui.Options{Interval: time.Hour})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})

	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range "alpine" {
		next, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})

	got := next.(tui.Model).View()
	if !strings.Contains(got, "alpine") {
		t.Errorf("filter dropped the match:\n%s", got)
	}
	if strings.Contains(got, "debian") {
		t.Errorf("filter kept a non-match:\n%s", got)
	}

	// Esc clears it and everything comes back.
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !strings.Contains(next.(tui.Model).View(), "debian") {
		t.Error("esc did not clear the filter")
	}
}

var _ = inventory.DefaultConfigPath
var _ = rexec.NewSSH
var _ = fleet.New

// TestRenderDetailScreens drives the M2 screens against the live fixtures.
// The systemd box is chosen deliberately: it is the only one whose service
// list is readable, and it carries a unit that always fails.
func TestRenderDetailScreens(t *testing.T) {
	f := newFleet(t)
	// systemd as PID 1 in a container does not boot in every environment,
	// and up.sh drops the box from the config when it does not. Skipping
	// beats failing on an absent fixture.
	if _, ok := f.Server("rove-fixture-ubuntu-systemd"); !ok {
		t.Skip("systemd fixture not running; see test/fixtures/sshd/up.sh output")
	}
	f.RefreshAll(context.Background())

	m := tui.New(f, tui.Options{Interval: time.Hour, ConfigPath: configPath})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})

	// Walk the cursor onto the systemd host.
	for i := 0; i < 12; i++ {
		if strings.Contains(next.(tui.Model).View(), "› ") {
			if sel := selectedName(next.(tui.Model)); strings.Contains(sel, "systemd") {
				break
			}
		}
		next, _ = next.Update(tea.KeyMsg{Type: tea.KeyDown})
	}

	for _, s := range []struct {
		key   rune
		name  string
		wants []string
	}{
		{'p', "processes", []string{"PID", "USER", "COMMAND"}},
		{'v', "services", []string{"UNIT", "STATE", "rove-broken"}},
		{'d', "disk", []string{"MOUNT", "USED", "FREE", "device"}},
	} {
		var cmd tea.Cmd
		next, cmd = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{s.key}})
		// Detail screens fetch asynchronously; run the command and deliver.
		if cmd != nil {
			if msg := cmd(); msg != nil {
				next, _ = next.Update(msg)
			}
		}
		frame := next.(tui.Model).View()
		t.Logf("\n%s", frame)

		for _, want := range s.wants {
			if !strings.Contains(frame, want) {
				t.Errorf("%s screen missing %q", s.name, want)
			}
		}
		for i, line := range strings.Split(frame, "\n") {
			if w := len([]rune(line)); w > 110 {
				t.Errorf("%s screen line %d is %d columns", s.name, i, w)
			}
		}
	}
}

// selectedName reads the highlighted host out of a rendered frame.
func selectedName(m tui.Model) string {
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "›") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				return fields[2]
			}
		}
	}
	return ""
}
