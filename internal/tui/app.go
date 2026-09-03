// Package tui is the interface. It imports fleet and model and nothing
// below them: it never sees ssh, the probe script, or an exec.Command.
package tui

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/cuonggt/rove/internal/action"
	"github.com/cuonggt/rove/internal/fleet"
	"github.com/cuonggt/rove/internal/model"
)

// Options configure a run. Everything here has a working default.
type Options struct {
	// Interval between automatic refreshes.
	Interval time.Duration
	// ConfigPath is passed to the interactive ssh session only when the user
	// asked for a non-default config. In normal use it is empty, so dropping
	// into a shell runs a bare `ssh <host>` with the user's own settings and
	// nothing of ours.
	ConfigPath string
	// ASCII swaps box-drawing and braille for characters that survive any
	// terminal and any font.
	ASCII bool
}

const (
	// tickInterval drives the spinner and the "N ago" clock. Refreshes are
	// scheduled off elapsed time rather than off a tick count, so changing
	// this does not change how often hosts are probed.
	tickInterval = 150 * time.Millisecond
	minWidth     = 60
)

type tickMsg time.Time

// hostRefreshedMsg reports one host finishing. Hosts are refreshed
// individually rather than as a batch so the table fills in as answers
// arrive instead of appearing all at once at the speed of the slowest host.
type hostRefreshedMsg struct{ name string }

type sshFinishedMsg struct{ err error }

// screen is which view is on top. Detail screens all belong to whichever
// host the fleet cursor is on, so there is no stack to keep: esc always
// returns to the fleet.
type screen int

const (
	screenFleet screen = iota
	screenOverview
	screenProcesses
	screenServices
	screenDisk
	screenLogs
	screenPorts
	screenContainers
	screenDiskUsage
	screenProcDetail
)

// detail collections arrive asynchronously, like everything else.
// actionDoneMsg carries the outcome of something that changed a host.
type actionDoneMsg struct {
	host   string
	act    action.Action
	result action.Result
	err    error
}

type procDetailMsg struct {
	name   string
	pid    int
	detail model.ProcessDetail
	err    error
}

type duMsg struct {
	name  string
	path  string
	usage model.DirUsage
	err   error
}

type containersMsg struct {
	name string
	list model.ContainerList
	err  error
}

type portsMsg struct {
	name string
	list model.PortList
	err  error
}

type logsMsg struct {
	name string
	unit string
	tail model.LogTail
	err  error
}

type processesMsg struct {
	name string
	list model.ProcessList
	err  error
}

type servicesMsg struct {
	name string
	list model.ServiceList
	err  error
}

// Model is the root. There are two screens and no router: a bool is enough.
type Model struct {
	f    *fleet.Fleet
	opts Options
	g    glyphs

	width, height int

	// cursor is a host name, never an index. Sort order and the filter both
	// change which row sits where; tracking a position would move the
	// selection under the user every time a probe lands.
	cursor string
	view   screen

	// Detail collections are cached per host and refetched when the screen
	// is opened, so switching back to a host does not stare at an empty
	// table while the round trip happens.
	// menu holds the actions offered for the selected row; pending holds
	// the one chosen and awaiting confirmation. Both nil means the
	// interface is read-only, which is its resting state.
	menu    []action.Action
	menuIdx int
	pending *pendingAction
	actNote string
	actOK   bool

	procDet  map[string]model.ProcessDetail
	procDErr map[string]string
	procPID  int

	du     map[string]model.DirUsage
	duErr  map[string]string
	duPath string
	// duStack is where drilling came from, so backspace walks back out the
	// way it came in rather than guessing at a parent path.
	duStack  []string
	conts    map[string]model.ContainerList
	contErr  map[string]string
	ports    map[string]model.PortList
	portErr  map[string]string
	logs     map[string]model.LogTail
	logErr   map[string]string
	logUnit  string
	procs    map[string]model.ProcessList
	procErr  map[string]string
	svcs     map[string]model.ServiceList
	svcErr   map[string]string
	loading  map[string]bool
	rowIndex int
	rowTop   int

	filtering bool
	filter    string

	inflight    map[string]bool
	frame       int
	lastRefresh time.Time
	shellErr    error
}

func New(f *fleet.Fleet, opts Options) Model {
	if opts.Interval <= 0 {
		opts.Interval = 30 * time.Second
	}
	g := unicodeGlyphs
	if opts.ASCII {
		g = asciiGlyphs
	}
	m := Model{
		f:        f,
		opts:     opts,
		g:        g,
		inflight: map[string]bool{},
		procDet:  map[string]model.ProcessDetail{},
		procDErr: map[string]string{},
		du:       map[string]model.DirUsage{},
		duErr:    map[string]string{},
		conts:    map[string]model.ContainerList{},
		contErr:  map[string]string{},
		ports:    map[string]model.PortList{},
		portErr:  map[string]string{},
		logs:     map[string]model.LogTail{},
		logErr:   map[string]string{},
		procs:    map[string]model.ProcessList{},
		procErr:  map[string]string{},
		svcs:     map[string]model.ServiceList{},
		svcErr:   map[string]string{},
		loading:  map[string]bool{},
		// Stamped now so the probes Init starts count as this round; a zero
		// value would read as overdue and schedule a second round at once.
		lastRefresh: time.Now(),
	}
	if names := f.Names(); len(names) > 0 {
		m.cursor = names[0]
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.refreshCmds(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// refreshCmds queues one command per host. Each runs concurrently; the fleet
// bounds how many actually talk to a server at once.
//
// It deliberately does not stamp lastRefresh. Model is passed by value, so a
// write here would land on a copy and be discarded, leaving the schedule
// permanently overdue and re-queueing a refresh on every frame. The callers
// stamp it on the model they return.
func (m Model) refreshCmds() tea.Cmd {
	names := m.f.Names()
	cmds := make([]tea.Cmd, 0, len(names))
	for _, name := range names {
		if m.inflight[name] {
			continue
		}
		m.inflight[name] = true
		cmds = append(cmds, m.refreshOne(name))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m Model) refreshOne(name string) tea.Cmd {
	f := m.f
	return func() tea.Msg {
		f.Refresh(context.Background(), name)
		return hostRefreshedMsg{name: name}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		m.frame++
		var cmds []tea.Cmd
		cmds = append(cmds, tick())
		// Refreshing is scheduled here rather than on its own timer so that
		// a slow round never overlaps itself: nothing is queued while a
		// probe is still in flight.
		if len(m.inflight) == 0 && time.Since(m.lastRefresh) >= m.opts.Interval {
			if c := m.refreshCmds(); c != nil {
				m.lastRefresh = time.Now()
				cmds = append(cmds, c)
			}
		}
		return m, tea.Batch(cmds...)

	case hostRefreshedMsg:
		delete(m.inflight, msg.name)
		return m, nil

	case actionDoneMsg:
		m.pending, m.menu = nil, nil
		switch {
		case msg.err != nil:
			m.actOK, m.actNote = false, msg.err.Error()
		case !msg.result.OK:
			m.actOK, m.actNote = false, orDefault(msg.result.Err, "it did not work, and did not say why")
		default:
			m.actOK = true
			m.actNote = msg.act.Summary(msg.host) + ": done"
			if msg.result.State != "" {
				m.actNote += ", now " + msg.result.State
			}
		}
		return m, nil

	case procDetailMsg:
		key := procKey(msg.name, msg.pid)
		delete(m.loading, "procd:"+key)
		if msg.err != nil {
			m.procDErr[key] = msg.err.Error()
		} else {
			delete(m.procDErr, key)
			m.procDet[key] = msg.detail
		}
		return m, nil

	case duMsg:
		key := logKey(msg.name, msg.path)
		delete(m.loading, "du:"+key)
		if msg.err != nil {
			m.duErr[key] = msg.err.Error()
		} else {
			delete(m.duErr, key)
			m.du[key] = msg.usage
		}
		return m, nil

	case containersMsg:
		delete(m.loading, "cont:"+msg.name)
		if msg.err != nil {
			m.contErr[msg.name] = msg.err.Error()
		} else {
			delete(m.contErr, msg.name)
			m.conts[msg.name] = msg.list
		}
		return m, nil

	case portsMsg:
		delete(m.loading, "port:"+msg.name)
		if msg.err != nil {
			m.portErr[msg.name] = msg.err.Error()
		} else {
			delete(m.portErr, msg.name)
			m.ports[msg.name] = msg.list
		}
		return m, nil

	case logsMsg:
		key := logKey(msg.name, msg.unit)
		delete(m.loading, "log:"+key)
		if msg.err != nil {
			m.logErr[key] = msg.err.Error()
		} else {
			delete(m.logErr, key)
			m.logs[key] = msg.tail
		}
		return m, nil

	case processesMsg:
		delete(m.loading, "proc:"+msg.name)
		if msg.err != nil {
			m.procErr[msg.name] = msg.err.Error()
		} else {
			delete(m.procErr, msg.name)
			m.procs[msg.name] = msg.list
		}
		return m, nil

	case servicesMsg:
		delete(m.loading, "svc:"+msg.name)
		if msg.err != nil {
			m.svcErr[msg.name] = msg.err.Error()
		} else {
			delete(m.svcErr, msg.name)
			m.svcs[msg.name] = msg.list
		}
		return m, nil

	case sshFinishedMsg:
		m.shellErr = msg.err
		// The host was very likely touched by hand; show it fresh.
		if m.cursor != "" && !m.inflight[m.cursor] {
			m.inflight[m.cursor] = true
			return m, m.refreshOne(m.cursor)
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.shellErr = nil

	// While filtering, almost every key is text. Only the keys that end
	// filtering are intercepted.
	if m.filtering {
		switch msg.Type {
		case tea.KeyEnter:
			m.filtering = false
			return m, nil
		case tea.KeyEsc:
			m.filtering, m.filter = false, ""
			m.clampCursor()
			return m, nil
		case tea.KeyBackspace:
			if r := []rune(m.filter); len(r) > 0 {
				m.filter = string(r[:len(r)-1])
			}
			m.clampCursor()
			return m, nil
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyRunes, tea.KeySpace:
			m.filter += string(msg.Runes)
			if msg.Type == tea.KeySpace {
				m.filter += " "
			}
			m.clampCursor()
			return m, nil
		}
		return m, nil
	}

	// A confirmation prompt owns the keyboard while it is up. Without
	// this, "y" would scroll a list behind the question it is answering.
	if m.pending != nil {
		return m.updatePending(msg)
	}
	if m.menu != nil {
		return m.updateMenu(msg)
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "esc":
		if m.view != screenFleet {
			m.view = screenFleet
			m.duStack, m.duPath = nil, ""
			m.resetRows()
		} else if m.filter != "" {
			m.filter = ""
			m.clampCursor()
		}
		return m, nil

	case "up", "k":
		if m.view == screenFleet {
			m.move(-1)
		} else {
			m.moveRow(-1)
		}
		return m, nil
	case "down", "j":
		if m.view == screenFleet {
			m.move(1)
		} else {
			m.moveRow(1)
		}
		return m, nil
	case "g", "home":
		if v := m.visible(); len(v) > 0 {
			m.cursor = v[0].Server.Name
		}
		return m, nil
	case "G", "end":
		if v := m.visible(); len(v) > 0 {
			m.cursor = v[len(v)-1].Server.Name
		}
		return m, nil

	case "/":
		m.filtering = true
		return m, nil

	case "enter":
		switch m.view {
		case screenDisk:
			return m.drillInto(m.mountUnderCursor(), true)
		case screenDiskUsage:
			return m.drillInto(m.dirUnderCursor(), true)
		case screenProcesses:
			return m.openProcess(m.pidUnderCursor())
		default:
			if _, ok := m.selected(); ok {
				m.view = screenOverview
				m.resetRows()
			}
		}
		return m, nil

	case "backspace", "left":
		// Walk back out of a drill-down one level at a time; leaving the
		// screen entirely is what esc is for.
		if m.view == screenProcDetail {
			m.view = screenProcesses
			m.resetRows()
			return m, nil
		}
		if m.view == screenDiskUsage && len(m.duStack) > 0 {
			prev := m.duStack[len(m.duStack)-1]
			m.duStack = m.duStack[:len(m.duStack)-1]
			if prev == "" {
				m.view = screenDisk
				m.resetRows()
				return m, nil
			}
			return m.drillInto(prev, false)
		}
		return m, nil

	case "o":
		if m.view != screenFleet {
			return m.openDetail(screenOverview)
		}
		return m, nil
	case "p":
		return m.openDetail(screenProcesses)
	case "v":
		return m.openDetail(screenServices)
	case "d":
		return m.openDetail(screenDisk)
	case "l":
		m.logUnit = m.unitUnderCursor()
		return m.openDetail(screenLogs)
	case "n":
		return m.openDetail(screenPorts)
	case "c":
		return m.openDetail(screenContainers)

	case "a":
		// The menu is built from the row under the cursor, so it can never
		// offer to restart a service while a process is highlighted.
		if acts := m.actionsFor(); len(acts) > 0 {
			m.menu, m.menuIdx = acts, 0
			m.actNote = ""
		}
		return m, nil

	case "r":
		if m.view == screenProcDetail {
			if h, ok := m.selected(); ok {
				delete(m.procDet, procKey(h.Server.Name, m.procPID))
			}
			return m.openProcess(m.procPID)
		}
		if m.view == screenDiskUsage {
			// A cached walk would make refresh a no-op, which is the one
			// thing refresh must never be.
			if h, ok := m.selected(); ok {
				delete(m.du, logKey(h.Server.Name, m.duPath))
			}
			return m.drillInto(m.duPath, false)
		}
		if m.view == screenProcesses || m.view == screenServices || m.view == screenLogs ||
			m.view == screenPorts || m.view == screenContainers {
			return m.openDetail(m.view)
		}
		m.lastRefresh = time.Now()
		return m, m.refreshCmds()

	case "s":
		return m, m.shell()
	}
	return m, nil
}

// openDetail switches to a detail screen and fetches what it needs. Disk
// needs nothing: the fleet snapshot already carries every filesystem.
func (m Model) openDetail(s screen) (tea.Model, tea.Cmd) {
	h, ok := m.selected()
	if !ok {
		return m, nil
	}
	m.view = s
	m.resetRows()

	switch s {
	case screenProcesses:
		key := "proc:" + h.Server.Name
		if m.loading[key] {
			return m, nil
		}
		m.loading[key] = true
		return m, m.fetchProcesses(h.Server.Name)
	case screenServices:
		key := "svc:" + h.Server.Name
		if m.loading[key] {
			return m, nil
		}
		m.loading[key] = true
		return m, m.fetchServices(h.Server.Name)
	case screenContainers:
		key := "cont:" + h.Server.Name
		if m.loading[key] {
			return m, nil
		}
		m.loading[key] = true
		return m, m.fetchContainers(h.Server.Name)
	case screenPorts:
		key := "port:" + h.Server.Name
		if m.loading[key] {
			return m, nil
		}
		m.loading[key] = true
		return m, m.fetchPorts(h.Server.Name)
	case screenLogs:
		key := "log:" + logKey(h.Server.Name, m.logUnit)
		if m.loading[key] {
			return m, nil
		}
		m.loading[key] = true
		return m, m.fetchLogs(h.Server.Name, m.logUnit)
	}
	return m, nil
}

// unitUnderCursor scopes a log request to the unit the reader is looking at.
// Anywhere else the question is about the host as a whole.
func (m Model) unitUnderCursor() string {
	if m.view != screenServices {
		return ""
	}
	h, ok := m.selected()
	if !ok {
		return ""
	}
	units := m.svcs[h.Server.Name].Ordered()
	if m.rowIndex < 0 || m.rowIndex >= len(units) {
		return ""
	}
	return units[m.rowIndex].Name
}

func logKey(host, unit string) string {
	if unit == "" {
		return host + "\x00"
	}
	return host + "\x00" + unit
}

// openProcess opens one process on the detail screen.
func (m Model) openProcess(pid int) (tea.Model, tea.Cmd) {
	h, ok := m.selected()
	if !ok || pid <= 0 {
		return m, nil
	}
	m.view = screenProcDetail
	m.procPID = pid
	m.resetRows()

	key := "procd:" + procKey(h.Server.Name, pid)
	if m.loading[key] {
		return m, nil
	}
	m.loading[key] = true
	return m, m.fetchProcDetail(h.Server.Name, pid)
}

// pidUnderCursor is the process the reader is pointing at.
func (m Model) pidUnderCursor() int {
	h, ok := m.selected()
	if !ok {
		return 0
	}
	procs := m.procs[h.Server.Name].Procs
	if m.rowIndex < 0 || m.rowIndex >= len(procs) {
		return 0
	}
	return procs[m.rowIndex].PID
}

func procKey(host string, pid int) string {
	return host + "\x00" + strconv.Itoa(pid)
}

// updateMenu drives the list of offered actions.
func (m Model) updateMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "a":
		m.menu = nil
		return m, nil
	case "up", "k":
		if m.menuIdx > 0 {
			m.menuIdx--
		}
		return m, nil
	case "down", "j":
		if m.menuIdx < len(m.menu)-1 {
			m.menuIdx++
		}
		return m, nil
	case "enter":
		h, ok := m.selected()
		if !ok {
			m.menu = nil
			return m, nil
		}
		m.pending = &pendingAction{act: m.menu[m.menuIdx], host: h.Server.Name}
		m.menu = nil
		return m, nil
	}
	return m, nil
}

// updatePending drives the confirmation. A write action takes a keystroke;
// a dangerous one takes a word, because "y" is muscle memory and typing
// "yes" is a decision.
func (m Model) updatePending(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := *m.pending

	switch msg.String() {
	case "esc", "ctrl+c":
		m.pending = nil
		return m, nil
	}

	if p.act.Dangerous() {
		switch msg.Type {
		case tea.KeyRunes:
			p.typed += string(msg.Runes)
		case tea.KeyBackspace:
			if p.typed != "" {
				p.typed = p.typed[:len(p.typed)-1]
			}
		case tea.KeyEnter:
			if p.ready() {
				m.pending = nil
				return m, m.runAction(p)
			}
			// Not the word: clear it rather than leaving a half-typed
			// answer that the next Enter might accept.
			p.typed = ""
		}
		m.pending = &p
		return m, nil
	}

	switch msg.String() {
	case "y", "enter":
		m.pending = nil
		return m, m.runAction(p)
	}
	return m, nil
}

// pendingAction is an action a person has chosen but not yet agreed to.
type pendingAction struct {
	act   action.Action
	host  string
	typed string
}

// A dangerous action asks for a word rather than a keystroke. "y" is
// muscle memory; typing "yes" is a decision.
const dangerousWord = "yes"

func (p pendingAction) ready() bool {
	if p.act.Dangerous() {
		return p.typed == dangerousWord
	}
	return true
}

// actionsFor lists what can be done to the row under the cursor. The menu
// is built from what is actually selected, so it can never offer to restart
// a service while a process is highlighted.
func (m Model) actionsFor() []action.Action {
	h, ok := m.selected()
	if !ok {
		return nil
	}
	switch m.view {
	case screenServices:
		units := m.svcs[h.Server.Name].Ordered()
		if m.rowIndex < 0 || m.rowIndex >= len(units) {
			return nil
		}
		u := units[m.rowIndex]
		return []action.Action{
			{Kind: action.ServiceRestart, Target: u.Name, Label: u.ShortName()},
			{Kind: action.ServiceStart, Target: u.Name, Label: u.ShortName()},
			{Kind: action.ServiceStop, Target: u.Name, Label: u.ShortName()},
		}
	case screenContainers:
		cs := m.conts[h.Server.Name].Ordered()
		if m.rowIndex < 0 || m.rowIndex >= len(cs) {
			return nil
		}
		c := cs[m.rowIndex]
		return []action.Action{
			{Kind: action.ContainerRestart, Target: c.ID, Label: c.Name},
			{Kind: action.ContainerStart, Target: c.ID, Label: c.Name},
			{Kind: action.ContainerStop, Target: c.ID, Label: c.Name},
		}
	case screenProcesses, screenProcDetail:
		pid, label := m.actionProcess()
		if pid <= 1 {
			// pid 1 is excluded in the action package too; not offering it
			// is friendlier than refusing it after the fact.
			return nil
		}
		t := strconv.Itoa(pid)
		return []action.Action{
			{Kind: action.ProcessTerm, Target: t, Label: label},
			{Kind: action.ProcessKill, Target: t, Label: label},
		}
	}
	return nil
}

func (m Model) actionProcess() (int, string) {
	h, ok := m.selected()
	if !ok {
		return 0, ""
	}
	if m.view == screenProcDetail {
		d := m.procDet[procKey(h.Server.Name, m.procPID)]
		return m.procPID, orDefault(d.Comm, strconv.Itoa(m.procPID))
	}
	procs := m.procs[h.Server.Name].Procs
	if m.rowIndex < 0 || m.rowIndex >= len(procs) {
		return 0, ""
	}
	p := procs[m.rowIndex]
	return p.PID, p.Command
}

func (m Model) runAction(p pendingAction) tea.Cmd {
	f := m.f
	return func() tea.Msg {
		res, err := f.Act(context.Background(), p.host, p.act, action.Confirm(p.host, p.act))
		return actionDoneMsg{host: p.host, act: p.act, result: res, err: err}
	}
}

func (m Model) fetchProcDetail(name string, pid int) tea.Cmd {
	f := m.f
	return func() tea.Msg {
		d, err := f.ProcessDetail(context.Background(), name, pid)
		return procDetailMsg{name: name, pid: pid, detail: d, err: err}
	}
}

// drillInto opens a path on the disk-usage screen, remembering where it
// came from when push is set.
func (m Model) drillInto(path string, push bool) (tea.Model, tea.Cmd) {
	h, ok := m.selected()
	if !ok || path == "" {
		return m, nil
	}
	if push {
		from := m.duPath
		if m.view == screenDisk {
			from = ""
		}
		m.duStack = append(m.duStack, from)
	}
	m.view = screenDiskUsage
	m.duPath = path
	m.resetRows()

	key := "du:" + logKey(h.Server.Name, path)
	if m.loading[key] {
		return m, nil
	}
	if _, cached := m.du[logKey(h.Server.Name, path)]; cached {
		return m, nil
	}
	m.loading[key] = true
	return m, m.fetchDu(h.Server.Name, path)
}

// mountUnderCursor is the filesystem the reader is pointing at.
func (m Model) mountUnderCursor() string {
	h, ok := m.selected()
	if !ok {
		return ""
	}
	disks := h.Snap.RealFilesystems()
	if m.rowIndex < 0 || m.rowIndex >= len(disks) {
		return ""
	}
	return disks[m.rowIndex].Mount
}

func (m Model) dirUnderCursor() string {
	h, ok := m.selected()
	if !ok {
		return ""
	}
	kids := m.du[logKey(h.Server.Name, m.duPath)].Children()
	if m.rowIndex < 0 || m.rowIndex >= len(kids) {
		return ""
	}
	return kids[m.rowIndex].Path
}

func (m Model) fetchDu(name, path string) tea.Cmd {
	f := m.f
	return func() tea.Msg {
		usage, err := f.DiskUsage(context.Background(), name, path)
		return duMsg{name: name, path: path, usage: usage, err: err}
	}
}

func (m Model) fetchContainers(name string) tea.Cmd {
	f := m.f
	return func() tea.Msg {
		list, err := f.Containers(context.Background(), name)
		return containersMsg{name: name, list: list, err: err}
	}
}

func (m Model) fetchPorts(name string) tea.Cmd {
	f := m.f
	return func() tea.Msg {
		list, err := f.Ports(context.Background(), name)
		return portsMsg{name: name, list: list, err: err}
	}
}

func (m Model) fetchLogs(name, unit string) tea.Cmd {
	f := m.f
	return func() tea.Msg {
		tail, err := f.Logs(context.Background(), name, unit, 0)
		return logsMsg{name: name, unit: unit, tail: tail, err: err}
	}
}

func (m Model) fetchProcesses(name string) tea.Cmd {
	f := m.f
	return func() tea.Msg {
		list, err := f.Processes(context.Background(), name)
		return processesMsg{name: name, list: list, err: err}
	}
}

func (m Model) fetchServices(name string) tea.Cmd {
	f := m.f
	return func() tea.Msg {
		list, err := f.Services(context.Background(), name)
		return servicesMsg{name: name, list: list, err: err}
	}
}

func (m *Model) resetRows() { m.rowIndex, m.rowTop = 0, 0 }

// moveRow walks a detail list and scrolls to keep the selection visible.
func (m *Model) moveRow(delta int) {
	n := m.rowCount()
	if n == 0 {
		return
	}
	m.rowIndex += delta
	if m.rowIndex < 0 {
		m.rowIndex = 0
	}
	if m.rowIndex >= n {
		m.rowIndex = n - 1
	}
	if m.rowIndex < m.rowTop {
		m.rowTop = m.rowIndex
	}
	if h := m.rowsVisible(); m.rowIndex >= m.rowTop+h {
		m.rowTop = m.rowIndex - h + 1
	}
}

// rowsVisible is how many list rows fit below the header and above the
// footer at the current terminal height.
func (m Model) rowsVisible() int {
	h := m.height - 8
	if h < 3 {
		return 3
	}
	return h
}

func (m Model) rowCount() int {
	h, ok := m.selected()
	if !ok {
		return 0
	}
	switch m.view {
	case screenProcesses:
		return len(m.procs[h.Server.Name].Procs)
	case screenServices:
		return len(m.svcs[h.Server.Name].Units)
	case screenDisk:
		return len(h.Snap.RealFilesystems())
	case screenLogs:
		return len(m.logs[logKey(h.Server.Name, m.logUnit)].Lines)
	case screenPorts:
		return len(m.ports[h.Server.Name].Listeners)
	case screenContainers:
		return len(m.conts[h.Server.Name].Containers)
	case screenDiskUsage:
		return len(m.du[logKey(h.Server.Name, m.duPath)].Children())
	}
	return 0
}

// shell suspends the interface and hands the terminal to ssh. On exit the
// alternate screen is restored and the host is reprobed.
func (m Model) shell() tea.Cmd {
	h, ok := m.selected()
	if !ok {
		return nil
	}
	args := []string{}
	if m.opts.ConfigPath != "" {
		args = append(args, "-F", m.opts.ConfigPath)
	}
	args = append(args, h.Server.Name)

	return tea.ExecProcess(exec.Command("ssh", args...), func(err error) tea.Msg {
		return sshFinishedMsg{err: err}
	})
}

// visible is the host list after filtering, in display order.
func (m Model) visible() []fleet.Host {
	all := m.f.Hosts()
	if m.filter == "" {
		return all
	}
	q := strings.ToLower(m.filter)
	out := make([]fleet.Host, 0, len(all))
	for _, h := range all {
		if matches(h, q) {
			out = append(out, h)
		}
	}
	return out
}

// matches searches the things a person would actually type: the host name,
// its environment, and its tags.
func matches(h fleet.Host, q string) bool {
	if strings.Contains(strings.ToLower(h.Server.Name), q) {
		return true
	}
	if strings.Contains(strings.ToLower(h.Server.Meta.Env), q) {
		return true
	}
	for _, t := range h.Server.Meta.Tags {
		if strings.Contains(strings.ToLower(t), q) {
			return true
		}
	}
	return false
}

func (m Model) selected() (fleet.Host, bool) {
	for _, h := range m.visible() {
		if h.Server.Name == m.cursor {
			return h, true
		}
	}
	return fleet.Host{}, false
}

func (m *Model) move(delta int) {
	v := m.visible()
	if len(v) == 0 {
		return
	}
	idx := 0
	for i, h := range v {
		if h.Server.Name == m.cursor {
			idx = i
			break
		}
	}
	idx += delta
	if idx < 0 {
		idx = 0
	}
	if idx >= len(v) {
		idx = len(v) - 1
	}
	m.cursor = v[idx].Server.Name
}

// clampCursor keeps the selection on a row that still exists after the
// filter changed.
func (m *Model) clampCursor() {
	v := m.visible()
	if len(v) == 0 {
		return
	}
	for _, h := range v {
		if h.Server.Name == m.cursor {
			return
		}
	}
	m.cursor = v[0].Server.Name
}

func (m Model) View() string {
	base := m.screenView()

	// A prompt replaces the screen rather than floating over it: a modal
	// drawn on top of a table that still responds to arrow keys is how
	// people confirm the wrong row.
	switch {
	case m.pending != nil:
		return base + "\n\n" + m.confirmView()
	case m.menu != nil:
		return base + "\n\n" + m.menuView()
	}
	if banner := m.actionBanner(); banner != "" {
		return banner + "\n\n" + base
	}
	return base
}

func (m Model) screenView() string {
	if m.width < minWidth {
		return dim.Render("rove needs a wider terminal\n")
	}
	if m.view == screenFleet {
		return m.fleetView()
	}
	h, ok := m.selected()
	if !ok {
		// The selected host disappeared underneath the detail view.
		return m.fleetView()
	}
	switch m.view {
	case screenOverview:
		return m.serverView(h)
	case screenProcesses:
		return m.processesView(h)
	case screenServices:
		return m.servicesView(h)
	case screenDisk:
		return m.diskView(h)
	case screenLogs:
		return m.logsView(h)
	case screenPorts:
		return m.portsView(h)
	case screenContainers:
		return m.containersView(h)
	case screenDiskUsage:
		return m.diskUsageView(h)
	case screenProcDetail:
		return m.procDetailView(h)
	}
	return m.fleetView()
}

// statusGlyph is the row marker. Healthy is quiet on purpose.
func (m Model) statusGlyph(h fleet.Host) (string, lipgloss.Style) {
	switch {
	case h.Status == model.StatusUnknown:
		return m.g.idle, dim
	case h.Status != model.StatusOK:
		return m.g.crit, brick
	case h.Note() != "":
		return m.g.warn, amber
	default:
		return m.g.ok, dim
	}
}
