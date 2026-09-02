package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/cuonggt/rove/internal/fleet"
	"github.com/cuonggt/rove/internal/model"
)

// layout is which columns fit at the current width. The note column is
// protected ahead of env, because a sentence saying what is wrong is worth
// more than a label saying where the host lives.
type layout struct {
	host, env, note int
	showEnv         bool
	showLoad        bool
	showNote        bool
}

const (
	numW   = 5 // "100%"
	loadW  = 6 // "12.34"
	gutter = 4 // cursor and status glyph, each with a trailing space
)

func (m Model) layoutFor(hosts []fleet.Host) layout {
	l := layout{}

	longest := 4
	longestEnv := 3
	for _, h := range hosts {
		if n := lipgloss.Width(h.Server.Name); n > longest {
			longest = n
		}
		if n := lipgloss.Width(h.Server.Meta.Env); n > longestEnv {
			longestEnv = n
		}
	}
	l.host = min(longest, 30)
	l.env = min(longestEnv, 12)

	l.showNote = m.width >= 72
	l.showLoad = m.width >= 84
	l.showEnv = m.width >= 100

	used := gutter + l.host + 1 + 3*(numW+1)
	if l.showLoad {
		used += loadW + 1
	}
	if l.showEnv {
		used += l.env + 1
	}
	l.note = m.width - used - 1
	if l.note < 12 {
		l.showNote = false
		l.note = 0
	}
	return l
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (m Model) fleetView() string {
	hosts := m.visible()
	l := m.layoutFor(m.f.Hosts())

	var b strings.Builder
	b.WriteString(m.titleBar())
	b.WriteString("\n\n")
	b.WriteString(m.headerRow(l))
	b.WriteString("\n")

	if len(hosts) == 0 {
		b.WriteString(dim.Render(fmt.Sprintf("  no host matches %q", m.filter)))
		b.WriteString("\n")
	}
	for _, h := range hosts {
		b.WriteString(m.hostRow(h, l))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.footer())
	return b.String()
}

func (m Model) titleBar() string {
	total, ok, attention := m.f.Summary()

	left := boldStyle.Render("rove")
	if m.shellErr != nil {
		left += "   " + brick.Render("ssh: "+m.shellErr.Error())
	}
	if m.filtering || m.filter != "" {
		cursor := ""
		if m.filtering {
			cursor = "▏"
			if m.opts.ASCII {
				cursor = "_"
			}
		}
		left += dim.Render("   /") + m.filter + accent.Render(cursor)
	}

	noun := "hosts"
	if total == 1 {
		noun = "host"
	}
	summary := fmt.Sprintf("%d %s %s %d ok", total, noun, m.g.sep, ok)
	if attention > 0 {
		summary += " " + m.g.sep + " " + amber.Render(fmt.Sprintf("%d need attention", attention))
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(summary)
	if gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + dim.Render(summary)
}

func (m Model) headerRow(l layout) string {
	var cells []string
	cells = append(cells, strings.Repeat(" ", gutter)+pad("HOST", l.host))
	if l.showEnv {
		cells = append(cells, pad("ENV", l.env))
	}
	cells = append(cells,
		padLeft("CPU", numW),
		padLeft("MEM", numW),
		padLeft("DISK", numW),
	)
	if l.showLoad {
		cells = append(cells, padLeft("LOAD", loadW))
	}
	if l.showNote {
		cells = append(cells, pad("NOTE", l.note))
	}
	return dim.Render(strings.Join(cells, " "))
}

func (m Model) hostRow(h fleet.Host, l layout) string {
	glyph, paint := m.statusGlyph(h)

	cursor := " "
	name := pad(h.Server.Name, l.host)
	if h.Server.Name == m.cursor {
		cursor = accent.Render(m.g.cursor)
		name = boldStyle.Render(name)
	}
	// A host still being probed shows the spinner in place of its marker,
	// so an unfinished round is visible rather than looking like a stall.
	if m.inflight[h.Server.Name] && !h.HasSnap {
		glyph, paint = m.g.spinner[m.frame%len(m.g.spinner)], accent
	}

	var cells []string
	cells = append(cells, cursor+" "+paint.Render(glyph)+" "+name)
	if l.showEnv {
		cells = append(cells, dim.Render(pad(h.Server.Meta.Env, l.env)))
	}
	cells = append(cells,
		m.numCell(h, cpuOf, numW),
		m.numCell(h, memOf, numW),
		m.numCell(h, diskOf, numW),
	)
	if l.showLoad {
		cells = append(cells, m.loadCell(h))
	}
	if l.showNote {
		cells = append(cells, m.noteCell(h, l.note))
	}
	return strings.Join(cells, " ")
}

// A metric reader returns the value and whether the host reported it. The
// distinction matters: a missing figure is em-dash, never zero.
type metric func(fleet.Host) (float64, bool)

func cpuOf(h fleet.Host) (float64, bool) { return h.CPUPct, h.HasCPU }

func memOf(h fleet.Host) (float64, bool) { return h.Snap.MemUsedPercent() }

func diskOf(h fleet.Host) (float64, bool) {
	fs, ok := h.Snap.FullestFilesystem()
	if !ok {
		return 0, false
	}
	return fs.UsedPercent(), true
}

func (m Model) numCell(h fleet.Host, read metric, w int) string {
	v, ok := read(h)
	if !ok {
		return dim.Render(padLeft(m.blank(), w))
	}
	text := padLeft(pctStr(v), w)
	if h.Stale() {
		// Figures from a host that has stopped answering are still the best
		// information available, but they must not read as current.
		return dim.Render(text)
	}
	return threshold(v).Render(text)
}

func (m Model) loadCell(h fleet.Host) string {
	if !h.Snap.HasLoad {
		return dim.Render(padLeft(m.blank(), loadW))
	}
	text := padLeft(fmt.Sprintf("%.2f", h.Snap.Load[0]), loadW)
	if h.Stale() {
		return dim.Render(text)
	}
	return loadStyle(h.Snap.Load[0], h.Snap.Cores).Render(text)
}

func (m Model) noteCell(h fleet.Host, w int) string {
	note := h.Note()
	if note == "" {
		return strings.Repeat(" ", w)
	}
	text := pad(note, w)
	switch {
	case h.Status != model.StatusOK:
		return brick.Render(text)
	default:
		return amber.Render(text)
	}
}

func (m Model) blank() string {
	if m.opts.ASCII {
		return "-"
	}
	return "—"
}

func (m Model) footer() string {
	keys := []string{
		accent.Render("↑↓") + " move",
		accent.Render("/") + " filter",
		accent.Render("⏎") + " inspect",
		accent.Render("s") + " shell",
		accent.Render("r") + " refresh",
		accent.Render("q") + " quit",
	}
	if m.opts.ASCII {
		keys[0] = accent.Render("jk") + " move"
		keys[2] = accent.Render("enter") + " inspect"
	}
	left := strings.Join(keys, "   ")

	right := ""
	switch {
	case len(m.inflight) > 0:
		right = fmt.Sprintf("%s probing %d", m.g.spinner[m.frame%len(m.g.spinner)], len(m.inflight))
	case !m.lastRefresh.IsZero():
		right = "updated " + shortSince(m.lastRefresh)
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		return dim.Render(left)
	}
	return dim.Render(left) + strings.Repeat(" ", gap) + dim.Render(right)
}
