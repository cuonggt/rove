package tui

import "github.com/charmbracelet/lipgloss"

// The palette is deliberately narrow. Healthy hosts are rendered in the
// terminal's own foreground colour and nothing else: if every row is
// coloured, the eye stops reading colour as a signal. Only warning and
// critical earn a hue.
var (
	dim = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{
		Light: "#6B7280", Dark: "#7C8986",
	})
	accent = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{
		Light: "#0F766E", Dark: "#4FB3A6",
	})
	amber = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{
		Light: "#9E6412", Dark: "#D79A47",
	})
	brick = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{
		Light: "#A33C2C", Dark: "#D96F5C",
	})

	plain     = lipgloss.NewStyle()
	boldStyle = lipgloss.NewStyle().Bold(true)
)

type glyphs struct {
	ok, warn, crit, idle string
	barFull, barEmpty    string
	spinner              []string
	cursor               string
	sep                  string
}

var unicodeGlyphs = glyphs{
	ok: "●", warn: "▲", crit: "✕", idle: "◌",
	barFull: "█", barEmpty: "░",
	spinner: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	cursor:  "›",
	sep:     "·",
}

var asciiGlyphs = glyphs{
	ok: "*", warn: "!", crit: "x", idle: ".",
	barFull: "#", barEmpty: "-",
	spinner: []string{"|", "/", "-", "\\"},
	cursor:  ">",
	sep:     "-",
}

// threshold picks a style from a percentage. The bands are the same
// everywhere so that a colour means one thing across the whole interface.
func threshold(pct float64) lipgloss.Style {
	switch {
	case pct >= 90:
		return brick
	case pct >= 75:
		return amber
	default:
		return plain
	}
}

// loadStyle judges load against core count, because a load of 4 is idle on a
// 16-core box and desperate on a single-core one.
func loadStyle(load float64, cores int) lipgloss.Style {
	if cores <= 0 {
		return plain
	}
	switch per := load / float64(cores); {
	case per >= 2:
		return brick
	case per >= 1:
		return amber
	default:
		return plain
	}
}
