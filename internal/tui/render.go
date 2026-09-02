package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// pad fits s to width columns, truncating with an ellipsis when it does not
// fit. Width is measured in display cells, not bytes, so box-drawing and
// wide characters in a hostname do not break the columns.
func pad(s string, width int) string {
	if width <= 0 {
		return ""
	}
	w := lipgloss.Width(s)
	if w == width {
		return s
	}
	if w < width {
		return s + strings.Repeat(" ", width-w)
	}
	if width == 1 {
		return "…"
	}
	out := []rune(s)
	for len(out) > 0 && lipgloss.Width(string(out)) > width-1 {
		out = out[:len(out)-1]
	}
	return pad(string(out)+"…", width)
}

// padLeft right-aligns, which is what every numeric column wants.
func padLeft(s string, width int) string {
	if width <= 0 {
		return ""
	}
	w := lipgloss.Width(s)
	if w >= width {
		return pad(s, width)
	}
	return strings.Repeat(" ", width-w) + s
}

// bar draws a proportional meter. It is the one place a percentage becomes
// something the eye reads before the mind does.
func bar(pct float64, cells int, g glyphs) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct/100*float64(cells) + 0.5)
	if filled > cells {
		filled = cells
	}
	return strings.Repeat(g.barFull, filled) + strings.Repeat(g.barEmpty, cells-filled)
}

func pctStr(v float64) string { return fmt.Sprintf("%.0f%%", v) }

// humanKB turns kilobytes into something a person reads at a glance.
func humanKB(kb int64) string {
	const unit = 1024.0
	v := float64(kb)
	for _, suffix := range []string{"KB", "MB", "GB", "TB"} {
		if v < unit || suffix == "TB" {
			if v >= 100 || suffix == "KB" {
				return fmt.Sprintf("%.0f %s", v, suffix)
			}
			return fmt.Sprintf("%.1f %s", v, suffix)
		}
		v /= unit
	}
	return ""
}

// shortDuration is for ages shown beside data: how old the reading is.
func shortDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

// ago phrases a duration for display. "just now" is already a phrase and
// must not collect a trailing "ago".
func ago(d time.Duration) string {
	s := shortDuration(d)
	if s == "just now" {
		return s
	}
	return s + " ago"
}

func shortSince(t time.Time) string { return ago(time.Since(t)) }

// humanSeconds formats uptime the way uptime(1) means it, not as a duration.
func humanSeconds(s int64) string {
	switch {
	case s <= 0:
		return "—"
	case s < 3600:
		return fmt.Sprintf("%dm", s/60)
	case s < 86400:
		return fmt.Sprintf("%dh %dm", s/3600, (s%3600)/60)
	default:
		return fmt.Sprintf("%dd %dh", s/86400, (s%86400)/3600)
	}
}
