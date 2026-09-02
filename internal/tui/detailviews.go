package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/cuonggt/rove/internal/fleet"
	"github.com/cuonggt/rove/internal/model"
)

// screenTitle is the common header for every detail screen: which host, and
// which of its screens you are on.
func (m Model) screenTitle(h fleet.Host, name string) string {
	glyph, paint := m.statusGlyph(h)
	left := paint.Render(glyph) + " " + boldStyle.Render(h.Server.Name) + dim.Render("   "+name)

	right := ""
	if m.loading["proc:"+h.Server.Name] || m.loading["svc:"+h.Server.Name] {
		right = m.g.spinner[m.frame%len(m.g.spinner)] + " reading"
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + dim.Render(right)
}

// detailKeys is the footer shared by the detail screens, with the current
// one marked so it is obvious where you are.
func (m Model) detailKeys(current screen) string {
	type entry struct {
		key, label string
		at         screen
	}
	entries := []entry{
		{"o", "overview", screenOverview},
		{"p", "processes", screenProcesses},
		{"v", "services", screenServices},
		{"d", "disk", screenDisk},
	}
	parts := make([]string, 0, len(entries)+2)
	for _, e := range entries {
		if e.at == current {
			parts = append(parts, boldStyle.Render(e.key+" "+e.label))
			continue
		}
		parts = append(parts, accent.Render(e.key)+" "+e.label)
	}
	parts = append(parts, accent.Render("s")+" shell", accent.Render("esc")+" back")
	return dim.Render(strings.Join(parts, "   "))
}

// footerWith right-aligns a status against the shared key list.
func (m Model) footerWith(current screen, right string) string {
	left := m.detailKeys(current)
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + dim.Render(right)
}

// window returns the slice of a list that fits on screen, and whether rows
// were cut off above or below.
func (m Model) window(n int) (from, to int) {
	h := m.rowsVisible()
	from = m.rowTop
	if from > n {
		from = 0
	}
	to = from + h
	if to > n {
		to = n
	}
	return from, to
}

func (m Model) scrollNote(n int) string {
	from, to := m.window(n)
	if n == 0 {
		return ""
	}
	if from == 0 && to == n {
		return fmt.Sprintf("%d rows", n)
	}
	return fmt.Sprintf("%d-%d of %d", from+1, to, n)
}

// ---------------------------------------------------------------- processes

func (m Model) processesView(h fleet.Host) string {
	var b strings.Builder
	b.WriteString(m.screenTitle(h, "processes"))
	b.WriteString("\n\n")

	if msg, ok := m.procErr[h.Server.Name]; ok {
		b.WriteString("  " + brick.Render("could not read the process table") + "\n")
		b.WriteString("  " + dim.Render(msg) + "\n\n")
		b.WriteString(m.footerWith(screenProcesses, ""))
		return b.String()
	}

	list, have := m.procs[h.Server.Name]
	if !have {
		b.WriteString(dim.Render("  reading the process table…"))
		b.WriteString("\n\n")
		b.WriteString(m.footerWith(screenProcesses, ""))
		return b.String()
	}
	if list.Err != "" {
		b.WriteString("  " + amber.Render(list.Err) + "\n\n")
		b.WriteString(m.footerWith(screenProcesses, ""))
		return b.String()
	}

	// Column widths: the command gets whatever is left, because it is the
	// only field whose length is unbounded.
	const pidW, userW, numColW, rssW = 7, 12, 6, 9
	cmdW := m.width - (2 + pidW + 1 + userW + 1 + numColW + 1 + numColW + 1 + rssW + 2)
	if cmdW < 16 {
		cmdW = 16
	}

	header := "  " + padLeft("PID", pidW) + " " + pad("USER", userW) + " " +
		padLeft("CPU", numColW) + " " + padLeft("MEM", numColW) + " " +
		padLeft("RSS", rssW) + "  " + pad("COMMAND", cmdW)
	b.WriteString(dim.Render(header))
	b.WriteString("\n")

	from, to := m.window(len(list.Procs))
	for i := from; i < to; i++ {
		p := list.Procs[i]
		cursor := " "
		if i == m.rowIndex {
			cursor = accent.Render(m.g.cursor)
		}
		b.WriteString(cursor + " " +
			padLeft(fmt.Sprint(p.PID), pidW) + " " +
			dim.Render(pad(p.User, userW)) + " " +
			m.procNum(p.CPU, p.HasCPU, numColW) + " " +
			m.procNum(p.Mem, p.HasMem, numColW) + " " +
			dim.Render(padLeft(rssText(p), rssW)) + "  " +
			pad(p.Command, cmdW))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.footerWith(screenProcesses, m.processFooter(list)))
	return b.String()
}

// processFooter says what was left out. A capped list that claims to be the
// whole table is worse than no list.
func (m Model) processFooter(list model.ProcessList) string {
	note := m.scrollNote(len(list.Procs))
	if list.Truncated {
		note = fmt.Sprintf("top %d of %d", len(list.Procs), list.Total)
		if !list.SortedRemotely {
			note += ", unsorted sample"
		}
	}
	return note
}

func (m Model) procNum(v float64, ok bool, w int) string {
	if !ok {
		return dim.Render(padLeft(m.blank(), w))
	}
	return threshold(v).Render(padLeft(fmt.Sprintf("%.1f", v), w))
}

func rssText(p model.Process) string {
	if !p.HasRSS {
		return "—"
	}
	return humanKB(p.RSSKB)
}

// ----------------------------------------------------------------- services

func (m Model) servicesView(h fleet.Host) string {
	var b strings.Builder
	b.WriteString(m.screenTitle(h, "services"))
	b.WriteString("\n\n")

	if msg, ok := m.svcErr[h.Server.Name]; ok {
		b.WriteString("  " + brick.Render("could not read the service list") + "\n")
		b.WriteString("  " + dim.Render(msg) + "\n\n")
		b.WriteString(m.footerWith(screenServices, ""))
		return b.String()
	}

	list, have := m.svcs[h.Server.Name]
	if !have {
		b.WriteString(dim.Render("  reading the service list…"))
		b.WriteString("\n\n")
		b.WriteString(m.footerWith(screenServices, ""))
		return b.String()
	}
	if !list.Supported() {
		b.WriteString("  " + amber.Render("init system: "+initLabel(list.Init)) + "\n")
		b.WriteString("  " + dim.Render("rove reads systemd and OpenRC; this host runs neither") + "\n\n")
		b.WriteString(m.footerWith(screenServices, ""))
		return b.String()
	}

	const stateW, subW = 10, 10
	nameW := 32
	descW := m.width - (2 + nameW + 1 + stateW + 1 + subW + 2)
	if descW < 12 {
		descW = 12
		nameW = maxInt(16, m.width-(2+1+stateW+1+subW+2+descW))
	}

	header := "  " + pad("UNIT", nameW) + " " + pad("STATE", stateW) + " " +
		pad("SUB", subW) + "  " + pad("DESCRIPTION", descW)
	b.WriteString(dim.Render(header))
	b.WriteString("\n")

	units := list.Ordered()
	from, to := m.window(len(units))
	for i := from; i < to; i++ {
		u := units[i]
		cursor := " "
		if i == m.rowIndex {
			cursor = accent.Render(m.g.cursor)
		}
		b.WriteString(cursor + " " +
			unitStyle(u).Render(pad(u.ShortName(), nameW)) + " " +
			unitStyle(u).Render(pad(u.Active, stateW)) + " " +
			dim.Render(pad(u.Sub, subW)) + "  " +
			dim.Render(pad(u.Description, descW)))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.footerWith(screenServices, m.serviceFooter(list)))
	return b.String()
}

func (m Model) serviceFooter(list model.ServiceList) string {
	note := m.scrollNote(len(list.Units))
	if n := len(list.FailedUnits()); n > 0 {
		note = fmt.Sprintf("%d failed  %s  %s", n, m.g.sep, note)
	}
	if list.State != "" && list.State != "running" {
		note = list.State + "  " + m.g.sep + "  " + note
	}
	return note
}

func unitStyle(u model.Unit) lipgloss.Style {
	switch {
	case u.Failed():
		return brick
	case u.Missing():
		return amber
	case u.Running():
		return plain
	default:
		return dim
	}
}

func initLabel(init string) string {
	if init == "" || init == "unknown" {
		return "not detected"
	}
	return init
}

// --------------------------------------------------------------------- disk

// diskView needs no round trip: the fleet snapshot already carries every
// filesystem, and asking again would be slower and no more accurate.
func (m Model) diskView(h fleet.Host) string {
	var b strings.Builder
	b.WriteString(m.screenTitle(h, "disk"))
	b.WriteString("\n\n")

	disks := h.Snap.RealFilesystems()
	if len(disks) == 0 {
		b.WriteString(dim.Render("  this host reported no filesystems"))
		b.WriteString("\n\n")
		b.WriteString(m.footerWith(screenDisk, ""))
		return b.String()
	}

	const usedW, freeW, pctW = 10, 10, 6
	barW := 20
	mountW := m.width - (2 + usedW + 1 + freeW + 1 + pctW + 1 + barW + 3)
	if mountW < 10 {
		mountW = 10
		barW = maxInt(6, m.width-(2+mountW+usedW+freeW+pctW+6))
	}

	header := "  " + pad("MOUNT", mountW) + " " + padLeft("USED", usedW) + " " +
		padLeft("FREE", freeW) + " " + padLeft("%", pctW) + "  " + pad("", barW)
	b.WriteString(dim.Render(header))
	b.WriteString("\n")

	from, to := m.window(len(disks))
	for i := from; i < to; i++ {
		fs := disks[i]
		cursor := " "
		if i == m.rowIndex {
			cursor = accent.Render(m.g.cursor)
		}
		used := fs.UsedPercent()
		style := threshold(used)
		b.WriteString(cursor + " " +
			pad(fs.Mount, mountW) + " " +
			dim.Render(padLeft(humanKB(fs.UsedKB), usedW)) + " " +
			dim.Render(padLeft(humanKB(fs.TotalKB-fs.UsedKB), freeW)) + " " +
			style.Render(padLeft(pctStr(used), pctW)) + "  " +
			style.Render(bar(used, barW, m.g)))
		b.WriteString("\n")
	}

	// The device is worth one line for the selected row rather than a column
	// that pushes the mount point off screen on every other row.
	if m.rowIndex >= 0 && m.rowIndex < len(disks) {
		b.WriteString("\n  " + dim.Render("device  "+disks[m.rowIndex].Device) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(m.footerWith(screenDisk, m.scrollNote(len(disks))))
	return b.String()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
