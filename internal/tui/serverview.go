package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/cuonggt/rove/internal/fleet"
	"github.com/cuonggt/rove/internal/model"
)

const barCells = 24

// serverView is the overview card: the answer to "what is going on with this
// one machine", without a shell.
func (m Model) serverView(h fleet.Host) string {
	var b strings.Builder

	b.WriteString(m.detailTitle(h))
	b.WriteString("\n\n")

	if h.Status != model.StatusOK {
		b.WriteString(m.problemBlock(h))
		b.WriteString("\n")
	}

	if !h.HasSnap {
		b.WriteString(dim.Render("  nothing has been read from this host yet"))
		b.WriteString("\n\n")
		b.WriteString(m.detailFooter(h))
		return b.String()
	}

	b.WriteString(m.identityBlock(h))
	b.WriteString("\n")
	b.WriteString(m.metersBlock(h))
	b.WriteString("\n")

	if disks := h.Snap.RealFilesystems(); len(disks) > 1 {
		b.WriteString(m.disksBlock(disks))
		b.WriteString("\n")
	}
	if len(h.Snap.RealInterfaces()) > 0 {
		b.WriteString(m.networkBlock(h))
		b.WriteString("\n")
	}
	if len(h.Snap.FailedUnits) > 0 {
		b.WriteString(m.unitsBlock(h))
		b.WriteString("\n")
	} else if h.Snap.Init != "" && !h.Snap.ServicesReadable() {
		b.WriteString("  " + amber.Render(m.g.warn+" service state unreadable") + "\n")
		b.WriteString("    " + dim.Render("init is "+initLabel(h.Snap.Init)+
			", but its unit list could not be queried") + "\n\n")
	}

	b.WriteString(m.detailFooter(h))
	return b.String()
}

func (m Model) detailTitle(h fleet.Host) string {
	glyph, paint := m.statusGlyph(h)
	left := paint.Render(glyph) + " " + boldStyle.Render(h.Server.Name)
	if env := h.Server.Meta.Env; env != "" {
		left += dim.Render("   " + env)
	}

	right := h.Server.Address
	if p := h.Server.Port; p != "" && p != "22" {
		right += ":" + p
	}
	if u := h.Server.User; u != "" {
		right = u + "@" + right
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + dim.Render(right)
}

// problemBlock states what is wrong and, where one exists, the command that
// resolves it. A reason without a remedy is only half an answer.
func (m Model) problemBlock(h fleet.Host) string {
	var b strings.Builder
	b.WriteString("  " + brick.Render(h.Reason))
	if h.Stale() {
		b.WriteString(dim.Render(fmt.Sprintf("   figures below are %s old", shortDuration(h.Age()))))
	}
	b.WriteString("\n")
	if h.Fix != "" {
		b.WriteString("  " + dim.Render("try") + "  " + accent.Render(h.Fix) + "\n")
	}
	return b.String()
}

func (m Model) identityBlock(h fleet.Host) string {
	s := h.Snap
	os := s.OS
	if os == "" {
		os = s.Kind
	}
	line := "  " + os
	if s.UptimeS > 0 {
		line += dim.Render("   up " + humanSeconds(s.UptimeS))
	}
	line += "\n  " + dim.Render(strings.TrimSpace(s.Arch+" "+m.g.sep+" "+s.Kernel))
	if s.Init != "" {
		line += dim.Render("  " + m.g.sep + "  init " + s.Init)
	}
	return line + "\n"
}

func (m Model) metersBlock(h fleet.Host) string {
	s := h.Snap
	var b strings.Builder

	if v, ok := h.CPUPct, h.HasCPU; ok {
		b.WriteString(m.meter("CPU", v, coresNote(s.Cores)))
	} else {
		// The core count is known from the first probe and is worth showing
		// even before there are two samples to take a delta between.
		why := "needs a second sample"
		if c := coresNote(s.Cores); c != "" {
			why = c + "  " + why
		}
		b.WriteString(m.meterMissing("CPU", why))
	}

	if v, ok := s.MemUsedPercent(); ok {
		used := s.MemTotalKB - s.MemAvailKB
		b.WriteString(m.meter("Memory", v, humanKB(used)+" / "+humanKB(s.MemTotalKB)))
	} else {
		b.WriteString(m.meterMissing("Memory", "not reported"))
	}

	if fs, ok := s.FullestFilesystem(); ok {
		note := fmt.Sprintf("%s   %s / %s", fs.Mount, humanKB(fs.UsedKB), humanKB(fs.TotalKB))
		b.WriteString(m.meter("Disk", fs.UsedPercent(), note))
	} else {
		b.WriteString(m.meterMissing("Disk", "not reported"))
	}

	if s.HasLoad {
		note := ""
		if s.Cores > 0 {
			note = fmt.Sprintf("%.2f per core", s.Load[0]/float64(s.Cores))
		}
		text := fmt.Sprintf("%.2f  %.2f  %.2f", s.Load[0], s.Load[1], s.Load[2])
		b.WriteString(fmt.Sprintf("  %s  %s   %s\n",
			pad("Load", 7),
			loadStyle(s.Load[0], s.Cores).Render(pad(text, barCells+6)),
			dim.Render(note)))
	}
	return b.String()
}

func (m Model) meter(label string, v float64, note string) string {
	style := threshold(v)
	return fmt.Sprintf("  %s  %s %s   %s\n",
		pad(label, 7),
		style.Render(bar(v, barCells, m.g)),
		style.Render(padLeft(pctStr(v), 5)),
		dim.Render(note))
}

func (m Model) meterMissing(label, why string) string {
	return fmt.Sprintf("  %s  %s %s   %s\n",
		pad(label, 7),
		dim.Render(strings.Repeat(m.g.barEmpty, barCells)),
		dim.Render(padLeft(m.blank(), 5)),
		dim.Render(why))
}

func coresNote(cores int) string {
	if cores <= 0 {
		return ""
	}
	if cores == 1 {
		return "1 core"
	}
	return fmt.Sprintf("%d cores", cores)
}

func (m Model) disksBlock(disks []model.Filesystem) string {
	var b strings.Builder
	b.WriteString("  " + dim.Render("Disks") + "\n")
	for _, fs := range disks {
		style := threshold(fs.UsedPercent())
		b.WriteString(fmt.Sprintf("    %s %s   %s\n",
			pad(fs.Mount, 22),
			style.Render(padLeft(pctStr(fs.UsedPercent()), 5)),
			dim.Render(humanKB(fs.UsedKB)+" / "+humanKB(fs.TotalKB))))
	}
	return b.String()
}

func (m Model) networkBlock(h fleet.Host) string {
	var b strings.Builder
	b.WriteString("  " + dim.Render("Network") + "\n")
	for _, i := range h.Snap.RealInterfaces() {
		b.WriteString("    " + pad(i.Name, 12) + dim.Render(i.Addr) + "\n")
	}
	return b.String()
}

func (m Model) unitsBlock(h fleet.Host) string {
	units := h.Snap.FailedUnits
	var b strings.Builder
	label := fmt.Sprintf("%d failed units", len(units))
	if len(units) == 1 {
		label = "1 failed unit"
	}
	b.WriteString("  " + amber.Render(m.g.warn+" "+label) + "\n")
	for _, u := range units {
		b.WriteString("    " + u + "\n")
	}
	return b.String()
}

func (m Model) detailFooter(h fleet.Host) string {
	right := ""
	if m.inflight[h.Server.Name] {
		right = m.g.spinner[m.frame%len(m.g.spinner)] + " probing"
	} else if !h.LastOK.IsZero() {
		right = "read " + ago(h.Age())
	}
	return m.footerWith(screenOverview, right)
}
