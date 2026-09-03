package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/cuonggt/rove/internal/action"
)

// overlay draws a bordered panel over the current screen. Actions are the
// only thing in rove that changes a machine, so they interrupt rather than
// appearing as one more line in a footer nobody reads.
func (m Model) overlay(title string, titleStyle lipgloss.Style, body []string) string {
	width := m.width - 8
	if width > 72 {
		width = 72
	}
	if width < 30 {
		width = 30
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(title))
	b.WriteString("\n\n")
	for _, line := range body {
		b.WriteString(line + "\n")
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(titleStyle.GetForeground()).
		Padding(1, 2).
		Width(width).
		Render(strings.TrimRight(b.String(), "\n"))
}

// menuView offers what can be done to the selected row, with each action's
// risk stated next to it rather than discovered afterwards.
func (m Model) menuView() string {
	h, ok := m.selected()
	if !ok {
		return ""
	}

	body := make([]string, 0, len(m.menu)+2)
	for i, a := range m.menu {
		cursor := "  "
		label := plain
		if i == m.menuIdx {
			cursor = accent.Render(m.g.cursor) + " "
			label = boldStyle
		}

		risk := dim.Render("write")
		if a.Dangerous() {
			risk = brick.Render("dangerous")
		}
		body = append(body, cursor+label.Render(pad(a.Summary(h.Server.Name), 46))+" "+risk)
	}
	body = append(body, "", dim.Render("↑↓ choose   enter select   esc cancel"))

	return m.overlay("What should rove do?", accent, body)
}

// confirmView asks the question. It names the machine and the specific
// consequence: "are you sure" teaches people to press y without reading.
func (m Model) confirmView() string {
	p := *m.pending

	body := []string{boldStyle.Render(p.act.Summary(p.host))}

	if c := p.act.Consequence(); c != "" {
		body = append(body, "", amber.Render(c))
	}

	body = append(body, "")
	if p.act.Dangerous() {
		typed := p.typed
		if typed == "" {
			typed = dim.Render("…")
		}
		body = append(body,
			brick.Render("This one is dangerous."),
			fmt.Sprintf("Type %s and press enter to go ahead:  %s",
				boldStyle.Render(dangerousWord), typed),
			"",
			dim.Render("esc cancels"))
		return m.overlay("Confirm", brick, body)
	}

	body = append(body, dim.Render("y or enter to go ahead   esc cancels"))
	return m.overlay("Confirm", amber, body)
}

// actionBanner reports what happened, once, above the screen it happened
// on. It stays until the next action rather than timing out, because a
// result that vanishes is a result somebody missed.
func (m Model) actionBanner() string {
	if m.actNote == "" {
		return ""
	}
	if m.actOK {
		return accent.Render("✓ ") + m.actNote
	}
	return brick.Render("✗ ") + m.actNote
}

var _ = action.RiskWrite
