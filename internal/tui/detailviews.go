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

// detailKeys renders the screen switcher at the widest form that fits.
//
// Every screen added to v0.2 lengthens this list, and a footer that grows
// until it squeezes out the status is a footer that silently stops
// reporting. So it degrades in steps instead: full labels, then tight
// labels, then bare keys.
func (m Model) detailKeys(current screen, avail int) string {
	type entry struct {
		key, label string
		at         screen
	}
	entries := []entry{
		{"o", "overview", screenOverview},
		{"p", "processes", screenProcesses},
		{"v", "services", screenServices},
		{"d", "disk", screenDisk},
		{"l", "logs", screenLogs},
		{"n", "ports", screenPorts},
	}

	render := func(sep string, labels bool) string {
		parts := make([]string, 0, len(entries)+2)
		for _, e := range entries {
			text := e.key
			if labels {
				text = e.key + " " + e.label
			}
			if e.at == current {
				parts = append(parts, boldStyle.Render(text))
				continue
			}
			if labels {
				parts = append(parts, accent.Render(e.key)+" "+e.label)
			} else {
				parts = append(parts, accent.Render(e.key))
			}
		}
		if labels {
			parts = append(parts, accent.Render("s")+" shell", accent.Render("esc")+" back")
		} else {
			parts = append(parts, accent.Render("s"), accent.Render("esc"))
		}
		return dim.Render(strings.Join(parts, sep))
	}

	for _, candidate := range []string{render("   ", true), render(" ", true), render(" ", false)} {
		if lipgloss.Width(candidate) <= avail {
			return candidate
		}
	}
	return render(" ", false)
}

// footerWith right-aligns a status against the key list, shrinking the keys
// rather than dropping the status.
func (m Model) footerWith(current screen, right string) string {
	avail := m.width - lipgloss.Width(right) - 2
	left := m.detailKeys(current, avail)
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
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

// --------------------------------------------------------------------- logs

// logsView shows the last lines of the journal, scoped to a unit when the
// reader came from a unit. It answers the question that always follows
// "backup-worker.service failed", which is "failed how".
func (m Model) logsView(h fleet.Host) string {
	var b strings.Builder

	title := "logs"
	if m.logUnit != "" {
		title = "logs  " + strings.TrimSuffix(m.logUnit, ".service")
	}
	b.WriteString(m.screenTitle(h, title))
	b.WriteString("\n\n")

	key := logKey(h.Server.Name, m.logUnit)

	if msg, ok := m.logErr[key]; ok {
		b.WriteString("  " + brick.Render("could not read the log") + "\n")
		b.WriteString("  " + dim.Render(msg) + "\n\n")
		b.WriteString(m.footerWith(screenLogs, ""))
		return b.String()
	}

	tail, have := m.logs[key]
	if !have {
		b.WriteString(dim.Render("  reading the log…"))
		b.WriteString("\n\n")
		b.WriteString(m.footerWith(screenLogs, ""))
		return b.String()
	}

	if !tail.Available() {
		b.WriteString("  " + amber.Render("no log to read") + "\n")
		if tail.Err != "" {
			b.WriteString("  " + dim.Render(tail.Err) + "\n")
		}
		b.WriteString("\n")
		b.WriteString(m.footerWith(screenLogs, ""))
		return b.String()
	}

	// A tail that looks complete but omits every system unit invites a
	// wrong conclusion, so the caveat goes above the lines, not below.
	if tail.Partial() {
		b.WriteString("  " + amber.Render("showing only this account's own messages") + "\n")
		b.WriteString("  " + dim.Render("add the login to the systemd-journal or adm group to see the rest") + "\n\n")
	}

	if len(tail.Lines) == 0 {
		b.WriteString("  " + dim.Render(orDefault(tail.Err, "no entries")) + "\n\n")
		b.WriteString(m.footerWith(screenLogs, ""))
		return b.String()
	}

	width := m.width - 4
	if width < 20 {
		width = 20
	}

	from, to := m.window(len(tail.Lines))
	for i := from; i < to; i++ {
		cursor := " "
		if i == m.rowIndex {
			cursor = accent.Render(m.g.cursor)
		}
		b.WriteString(cursor + " " + logLineStyle(tail.Lines[i]).Render(pad(tail.Lines[i], width)))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.footerWith(screenLogs, m.logFooter(tail)))
	return b.String()
}

func (m Model) logFooter(tail model.LogTail) string {
	note := m.scrollNote(len(tail.Lines))
	src := tail.Source
	if !tail.FromJournal() {
		// A scraped syslog file filters by substring, not by unit, so say
		// where the lines came from rather than implying journald accuracy.
		src = "file " + src
	}
	return src + "  " + m.g.sep + "  " + note
}

// logLineStyle lifts the lines a reader is scanning for out of the noise.
// It matches on the words an init system and a kernel actually use.
func logLineStyle(line string) lipgloss.Style {
	l := strings.ToLower(line)
	switch {
	case strings.Contains(l, "error"), strings.Contains(l, "fatal"),
		strings.Contains(l, "failed"), strings.Contains(l, "failure"),
		strings.Contains(l, "panic"), strings.Contains(l, "segfault"):
		return brick
	case strings.Contains(l, "warn"), strings.Contains(l, "denied"),
		strings.Contains(l, "timed out"), strings.Contains(l, "refused"):
		return amber
	}
	return dim
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// -------------------------------------------------------------------- ports

// portsView answers "what can reach this machine". The sockets facing the
// network sort first, because that is the half of the list anyone opening
// this screen actually came for.
func (m Model) portsView(h fleet.Host) string {
	var b strings.Builder
	b.WriteString(m.screenTitle(h, "ports"))
	b.WriteString("\n\n")

	if msg, ok := m.portErr[h.Server.Name]; ok {
		b.WriteString("  " + brick.Render("could not read the sockets") + "\n")
		b.WriteString("  " + dim.Render(msg) + "\n\n")
		b.WriteString(m.footerWith(screenPorts, ""))
		return b.String()
	}

	list, have := m.ports[h.Server.Name]
	if !have {
		b.WriteString(dim.Render("  reading the sockets…"))
		b.WriteString("\n\n")
		b.WriteString(m.footerWith(screenPorts, ""))
		return b.String()
	}
	if !list.Available() {
		b.WriteString("  " + amber.Render("no socket listing available") + "\n")
		if list.Err != "" {
			b.WriteString("  " + dim.Render(list.Err) + "\n")
		}
		b.WriteString("\n")
		b.WriteString(m.footerWith(screenPorts, ""))
		return b.String()
	}

	// The ports are complete either way; only the owners are missing. Say
	// exactly that, so a blank column is not read as "no process".
	if list.Limited {
		b.WriteString("  " + dim.Render("owners need root; the ports below are complete") + "\n\n")
	}

	const protoW, portW, scopeW = 6, 6, 8
	addrW := 22
	procW := m.width - (2 + protoW + 1 + addrW + 1 + portW + 1 + scopeW + 2)
	if procW < 10 {
		procW = 10
		addrW = maxInt(10, m.width-(2+protoW+1+portW+1+scopeW+2+procW))
	}

	header := "  " + pad("PROTO", protoW) + " " + pad("ADDRESS", addrW) + " " +
		padLeft("PORT", portW) + " " + pad("SCOPE", scopeW) + "  " + pad("PROCESS", procW)
	b.WriteString(dim.Render(header))
	b.WriteString("\n")

	listeners := list.Ordered()
	from, to := m.window(len(listeners))
	for i := from; i < to; i++ {
		l := listeners[i]
		cursor := " "
		if i == m.rowIndex {
			cursor = accent.Render(m.g.cursor)
		}

		scope, scopeStyle := "local", dim
		if l.Exposed() {
			// Not a warning: a web server on 0.0.0.0 is doing its job.
			// It is where the eye should land first, so it gets the accent.
			scope, scopeStyle = "network", accent
		}

		proc := dim.Render(pad(m.blank(), procW))
		if l.HasProcess {
			proc = pad(l.Process, procW)
		}

		b.WriteString(cursor + " " +
			dim.Render(pad(l.Proto, protoW)) + " " +
			pad(l.Addr, addrW) + " " +
			padLeft(fmt.Sprint(l.Port), portW) + " " +
			scopeStyle.Render(pad(scope, scopeW)) + "  " +
			proc)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.footerWith(screenPorts, m.portFooter(list)))
	return b.String()
}

func (m Model) portFooter(list model.PortList) string {
	parts := []string{fmt.Sprintf("%d listening", len(list.Listeners))}
	if n := list.ExposedCount(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d on the network", n))
	}
	if n := list.Established(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d established", n))
	}
	return strings.Join(parts, "  "+m.g.sep+"  ")
}
