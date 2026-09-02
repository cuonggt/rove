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
		{"c", "containers", screenContainers},
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

// --------------------------------------------------------------- containers

// containersView lists what a container runtime is running. Detection is
// three separate answers, not one: no runtime, a runtime that will not
// talk, and a runtime with nothing on it. Collapsing them into an empty
// table would tell the reader the wrong thing three different ways.
func (m Model) containersView(h fleet.Host) string {
	var b strings.Builder
	b.WriteString(m.screenTitle(h, "containers"))
	b.WriteString("\n\n")

	if msg, ok := m.contErr[h.Server.Name]; ok {
		b.WriteString("  " + brick.Render("could not ask about containers") + "\n")
		b.WriteString("  " + dim.Render(msg) + "\n\n")
		b.WriteString(m.footerWith(screenContainers, ""))
		return b.String()
	}

	list, have := m.conts[h.Server.Name]
	if !have {
		b.WriteString(dim.Render("  looking for a container runtime…"))
		b.WriteString("\n\n")
		b.WriteString(m.footerWith(screenContainers, ""))
		return b.String()
	}

	if !list.Available() {
		if list.Installed() {
			// The binary is there. That is worth saying, because the fix is
			// a daemon or a group, not an installation.
			b.WriteString("  " + amber.Render(list.CLI+" is installed but unusable") + "\n")
		} else {
			b.WriteString("  " + dim.Render("no container runtime on this host") + "\n")
		}
		if list.Err != "" {
			b.WriteString("  " + dim.Render(list.Err) + "\n")
		}
		b.WriteString("\n")
		b.WriteString(m.footerWith(screenContainers, ""))
		return b.String()
	}

	if len(list.Containers) == 0 {
		b.WriteString("  " + dim.Render(list.Source+" is running, with no containers") + "\n\n")
		b.WriteString(m.footerWith(screenContainers, m.containerFooter(list)))
		return b.String()
	}

	const stateW, portsW = 9, 22
	nameW, imageW := 24, 28
	statusW := m.width - (2 + stateW + 1 + nameW + 1 + imageW + 1 + portsW + 2)
	if statusW < 12 {
		statusW = 12
		imageW = maxInt(12, m.width-(2+stateW+1+nameW+1+portsW+2+statusW))
	}

	header := "  " + pad("STATE", stateW) + " " + pad("NAME", nameW) + " " +
		pad("IMAGE", imageW) + " " + pad("PORTS", portsW) + "  " + pad("STATUS", statusW)
	b.WriteString(dim.Render(header))
	b.WriteString("\n")

	containers := list.Ordered()
	from, to := m.window(len(containers))
	for i := from; i < to; i++ {
		c := containers[i]
		cursor := " "
		if i == m.rowIndex {
			cursor = accent.Render(m.g.cursor)
		}

		ports := dim.Render(pad(c.Ports, portsW))
		if c.Exposed() {
			// A published container port bypasses the host firewall rules
			// people assume are protecting them, so it gets the accent.
			ports = accent.Render(pad(c.Ports, portsW))
		}

		b.WriteString(cursor + " " +
			containerStyle(c).Render(pad(c.State, stateW)) + " " +
			pad(c.Name, nameW) + " " +
			dim.Render(pad(c.Image, imageW)) + " " +
			ports + "  " +
			dim.Render(pad(c.Status, statusW)))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.footerWith(screenContainers, m.containerFooter(list)))
	return b.String()
}

func (m Model) containerFooter(list model.ContainerList) string {
	parts := []string{fmt.Sprintf("%d running", list.RunningCount())}
	if stopped := len(list.Containers) - list.RunningCount(); stopped > 0 {
		parts = append(parts, fmt.Sprintf("%d stopped", stopped))
	}
	if n := list.ExposedCount(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d publishing", n))
	}
	if list.Version != "" {
		parts = append(parts, list.Source+" "+list.Version)
	}
	return strings.Join(parts, "  "+m.g.sep+"  ")
}

func containerStyle(c model.Container) lipgloss.Style {
	switch c.State {
	case "running":
		return plain
	case "exited", "dead":
		return brick
	case "paused":
		return amber
	default:
		return dim
	}
}

// --------------------------------------------------------------- drill-down

// diskUsageView answers what is filling a filesystem up. Every figure here
// can be a floor rather than a measurement -- an unprivileged du skips what
// it cannot read and still prints a plausible total -- so the screen says
// which it is before showing a single number.
func (m Model) diskUsageView(h fleet.Host) string {
	var b strings.Builder
	b.WriteString(m.screenTitle(h, "disk  "+m.duPath))
	b.WriteString("\n\n")

	key := logKey(h.Server.Name, m.duPath)

	if msg, ok := m.duErr[key]; ok {
		b.WriteString("  " + brick.Render("could not measure "+m.duPath) + "\n")
		b.WriteString("  " + dim.Render(msg) + "\n\n")
		b.WriteString(m.footerWith(screenDiskUsage, ""))
		return b.String()
	}

	usage, have := m.du[key]
	if !have {
		b.WriteString(dim.Render("  measuring " + m.duPath + "…"))
		b.WriteString("\n  " + dim.Render("walking a large filesystem takes a while; the probe caps itself at 20s") + "\n\n")
		b.WriteString(m.footerWith(screenDiskUsage, ""))
		return b.String()
	}
	if usage.Err != "" {
		b.WriteString("  " + amber.Render(usage.Err) + "\n\n")
		b.WriteString(m.footerWith(screenDiskUsage, ""))
		return b.String()
	}

	// The caveat goes above the numbers, because it changes what they mean.
	if !usage.Exact() {
		switch {
		case usage.TimedOut:
			b.WriteString("  " + amber.Render("the walk hit its time limit; these are floors, not totals") + "\n\n")
		case usage.Shallow:
			b.WriteString("  " + amber.Render("this du cannot descend; only the total is known") + "\n\n")
		default:
			b.WriteString("  " + amber.Render(fmt.Sprintf(
				"%d directories could not be read; every figure below is a floor", usage.Unreadable)) + "\n\n")
		}
	}

	kids := usage.Children()
	if len(kids) == 0 {
		b.WriteString("  " + dim.Render(fmt.Sprintf("%s holds %s, with nothing below it",
			m.duPath, humanKB(usage.Total()))) + "\n\n")
		b.WriteString(m.footerWith(screenDiskUsage, m.duFooter(usage)))
		return b.String()
	}

	const sizeW, shareW, barW = 10, 6, 16
	pathW := m.width - (2 + sizeW + 1 + shareW + 1 + barW + 3)
	if pathW < 16 {
		pathW = 16
	}

	header := "  " + padLeft("SIZE", sizeW) + " " + padLeft("SHARE", shareW) + " " +
		pad("", barW) + "  " + pad("PATH", pathW)
	b.WriteString(dim.Render(header))
	b.WriteString("\n")

	from, to := m.window(len(kids))
	for i := from; i < to; i++ {
		e := kids[i]
		cursor := " "
		if i == m.rowIndex {
			cursor = accent.Render(m.g.cursor)
		}
		share := usage.Share(e)
		// Shaded against the parent, so the one directory responsible for a
		// full disk is visible without reading a single number.
		style := plain
		if share >= 50 {
			style = amber
		}
		b.WriteString(cursor + " " +
			style.Render(padLeft(humanKB(e.KB), sizeW)) + " " +
			dim.Render(padLeft(fmt.Sprintf("%.0f%%", share), shareW)) + " " +
			style.Render(bar(share, barW, m.g)) + "  " +
			pad(e.Path, pathW))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.footerWith(screenDiskUsage, m.duFooter(usage)))
	return b.String()
}

func (m Model) duFooter(usage model.DirUsage) string {
	total := humanKB(usage.Total())
	if !usage.Exact() {
		total = "at least " + total
	}
	parts := []string{total}
	if n := len(usage.Children()); n > 0 {
		parts = append(parts, m.scrollNote(n))
	}
	parts = append(parts, "enter to descend")
	if len(m.duStack) > 0 {
		parts = append(parts, "backspace to go back")
	}
	return strings.Join(parts, "  "+m.g.sep+"  ")
}

// ----------------------------------------------------------- process detail

// procDetailView is what the process list cannot show: the whole command
// line, where the process came from, and whether it belongs to a container.
//
// It never shows the environment. /proc/PID/environ routinely holds
// database passwords and API keys, and a diagnostic is not worth putting
// those into somebody's scrollback.
func (m Model) procDetailView(h fleet.Host) string {
	var b strings.Builder
	key := procKey(h.Server.Name, m.procPID)

	title := fmt.Sprintf("process %d", m.procPID)
	b.WriteString(m.screenTitle(h, title))
	b.WriteString("\n\n")

	if msg, ok := m.procDErr[key]; ok {
		b.WriteString("  " + brick.Render("could not read the process") + "\n")
		b.WriteString("  " + dim.Render(msg) + "\n\n")
		b.WriteString(m.footerWith(screenProcesses, ""))
		return b.String()
	}

	d, have := m.procDet[key]
	if !have {
		b.WriteString(dim.Render("  reading the process…"))
		b.WriteString("\n\n")
		b.WriteString(m.footerWith(screenProcesses, ""))
		return b.String()
	}
	if !d.Found() {
		// A pid from a list taken seconds ago may already be gone. That is
		// an answer, not a failure.
		b.WriteString("  " + amber.Render(orDefault(d.Err, "this process is gone")) + "\n\n")
		b.WriteString(m.footerWith(screenProcesses, ""))
		return b.String()
	}

	name := d.Comm
	if name == "" {
		name = "process"
	}
	b.WriteString("  " + boldStyle.Render(name))
	if d.Zombie() {
		// Worth calling out: nothing can be done to a zombie. The parent
		// never reaped it, so the parent is the problem.
		b.WriteString("  " + brick.Render("zombie, waiting to be reaped by "+orDefault(d.Parent, "its parent")))
	}
	b.WriteString("\n\n")

	if d.Cmdline != "" {
		for i, line := range wrapText(d.Cmdline, m.width-4) {
			label := "  "
			if i > 0 {
				label = "  "
			}
			b.WriteString(label + dim.Render(line) + "\n")
		}
		b.WriteString("\n")
	}

	rows := [][2]string{
		{"state", d.StateLabel()},
		{"user", orDefault(d.User, fmt.Sprintf("uid %d", d.UID))},
		{"parent", parentText(d)},
		{"threads", fmt.Sprint(d.Threads)},
		{"running for", humanSeconds(d.ElapsedS)},
		{"memory", memText(d)},
	}
	if d.HasFDs {
		rows = append(rows, [2]string{"open files", fmt.Sprint(d.FDs)})
	}
	if d.Exe != "" {
		rows = append(rows, [2]string{"executable", d.Exe})
	}
	if d.Cwd != "" {
		rows = append(rows, [2]string{"working dir", d.Cwd})
	}

	for _, r := range rows {
		if r[1] == "" {
			continue
		}
		b.WriteString("  " + dim.Render(pad(r[0], 14)) + " " + r[1] + "\n")
	}

	// The reason the container work matters: on a container host most of
	// this list belongs to containers, and ps says nothing about it.
	if d.InContainer() {
		b.WriteString("\n  " + accent.Render("in container") + " " + d.ShortContainer() + "\n")
		b.WriteString("  " + dim.Render("press c for the container list") + "\n")
	}

	if d.Limited {
		b.WriteString("\n  " + dim.Render("some fields need root or the owning account and were not read") + "\n")
	}

	b.WriteString("\n")
	b.WriteString(m.footerWith(screenProcesses, "backspace for the list"))
	return b.String()
}

func parentText(d model.ProcessDetail) string {
	if d.PPid == 0 {
		return ""
	}
	if d.Parent != "" {
		return fmt.Sprintf("%s (%d)", d.Parent, d.PPid)
	}
	return fmt.Sprint(d.PPid)
}

func memText(d model.ProcessDetail) string {
	if d.RSSKB <= 0 {
		return ""
	}
	if d.VSZKB > 0 {
		return fmt.Sprintf("%s resident, %s virtual", humanKB(d.RSSKB), humanKB(d.VSZKB))
	}
	return humanKB(d.RSSKB) + " resident"
}

// wrapText breaks a long command line across lines rather than truncating
// it: the argument that explains what a process is doing is usually at the
// end, which is exactly what a truncation removes.
func wrapText(s string, width int) []string {
	if width < 20 {
		width = 20
	}
	var out []string
	for len(s) > width {
		cut := strings.LastIndex(s[:width], " ")
		if cut <= 0 {
			cut = width
		}
		out = append(out, s[:cut])
		s = strings.TrimLeft(s[cut:], " ")
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}
