// Package fleet is the service layer between the interface and the transport.
// The TUI imports only this and model; it never sees exec or probe.
package fleet

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	rexec "github.com/cuonggt/rove/internal/exec"
	"github.com/cuonggt/rove/internal/model"
	"github.com/cuonggt/rove/internal/probe"
)

// Host is everything known about one server, including what was true the
// last time it answered. A failing host keeps its last figures rather than
// blanking, because the moment something breaks is exactly when the last
// known state is most useful.
type Host struct {
	Server model.Server
	Status model.Status
	Reason string
	Fix    string

	Snap    model.Snapshot
	HasSnap bool

	CPUPct float64
	HasCPU bool

	LastOK time.Time

	prevCPU model.CPUStat
	hasPrev bool
}

// Age is how long ago this host last answered.
func (h Host) Age() time.Duration {
	if h.LastOK.IsZero() {
		return 0
	}
	return time.Since(h.LastOK)
}

// Stale reports whether the figures shown are from a probe that has since
// stopped succeeding.
func (h Host) Stale() bool {
	return h.HasSnap && h.Status != model.StatusOK
}

// Note is the one line that says what is worth knowing about this host. A
// number a reader has to interpret is worse than a sentence.
func (h Host) Note() string {
	if h.Status != model.StatusOK {
		if h.Stale() {
			return fmt.Sprintf("%s, %s ago", h.Reason, short(h.Age()))
		}
		return h.Reason
	}
	if n := len(h.Snap.FailedUnits); n > 0 {
		if n == 1 {
			return h.Snap.FailedUnits[0] + " failed"
		}
		return fmt.Sprintf("%d failed units", n)
	}
	if fs, ok := h.Snap.FullestFilesystem(); ok && fs.UsedPercent() >= 85 {
		return fmt.Sprintf("%s %.0f%% full", fs.Mount, fs.UsedPercent())
	}
	if h.Snap.HasLoad && h.Snap.Cores > 0 {
		if per := h.Snap.Load[0] / float64(h.Snap.Cores); per >= 2 {
			return fmt.Sprintf("load %.1fx cores", per)
		}
	}
	// Last, because it is a caveat rather than a symptom: this host may be
	// perfectly fine, but part of the health picture could not be read and
	// silence would imply otherwise.
	if h.Snap.Init == "systemd" && !h.Snap.ServicesReadable() {
		return "service state unreadable"
	}
	return ""
}

// NeedsAttention is what the summary line counts. Healthy hosts should be
// quiet; only these are worth colour.
func (h Host) NeedsAttention() bool {
	return h.Status != model.StatusOK || h.Note() != ""
}

func short(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

// Fleet holds the state of every known server and refreshes it.
type Fleet struct {
	ex          rexec.Executor
	timeout     time.Duration
	concurrency int

	// sem bounds concurrent probes. It lives on the Fleet rather than in
	// RefreshAll because the interface refreshes hosts individually, so it
	// would otherwise fan out unbounded and fire a prompt per host.
	sem chan struct{}

	mu    sync.RWMutex
	hosts map[string]*Host
	order []string
}

func New(ex rexec.Executor, servers []model.Server, concurrency int, timeout time.Duration) *Fleet {
	if concurrency < 1 {
		concurrency = 1
	}
	f := &Fleet{
		ex:          ex,
		timeout:     timeout,
		concurrency: concurrency,
		sem:         make(chan struct{}, concurrency),
		hosts:       make(map[string]*Host, len(servers)),
	}
	for _, s := range servers {
		f.hosts[s.Name] = &Host{Server: s, Status: model.StatusUnknown}
		f.order = append(f.order, s.Name)
	}
	sort.Strings(f.order)
	return f
}

// Hosts returns a stable snapshot of current state, in name order.
func (f *Fleet) Hosts() []Host {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]Host, 0, len(f.order))
	for _, name := range f.order {
		out = append(out, *f.hosts[name])
	}
	return out
}

func (f *Fleet) Len() int { return len(f.order) }

// Names returns every host name, in the order the interface shows them.
func (f *Fleet) Names() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return append([]string(nil), f.order...)
}

// RefreshAll probes every host concurrently. Each host has its own deadline,
// so one unreachable machine cannot hold up the rest.
func (f *Fleet) RefreshAll(ctx context.Context) {
	f.mu.RLock()
	names := append([]string(nil), f.order...)
	f.mu.RUnlock()

	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			f.Refresh(ctx, name)
		}(name)
	}
	wg.Wait()
}

// Refresh probes one host and folds the result into its state.
func (f *Fleet) Refresh(ctx context.Context, name string) {
	f.mu.RLock()
	h, ok := f.hosts[name]
	f.mu.RUnlock()
	if !ok {
		return
	}

	select {
	case f.sem <- struct{}{}:
		defer func() { <-f.sem }()
	case <-ctx.Done():
		return
	}

	hctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	snap, err := probe.Run(hctx, f.ex, h.Server.Target())

	f.mu.Lock()
	defer f.mu.Unlock()

	if err != nil {
		if hctx.Err() != nil {
			err = context.DeadlineExceeded
		}
		// A host that connects but returns nothing usable is its own state.
		// Restricted shells, forced commands and gateways with nothing
		// behind them all land here, and "unreachable" would be wrong for
		// every one of them: the connection worked.
		if errors.Is(err, probe.ErrNoHeader) {
			h.Status = model.StatusProbeError
			h.Reason = "connected, but ran no shell"
			h.Fix = "ssh " + h.Server.Name + " uname -a"
			return
		}
		stderr := rexec.StderrOf(err)
		if stderr == "" {
			// Not a transport failure: the host answered with something we
			// could not read. Report the parse failure itself.
			stderr = err.Error()
		}
		h.Status, h.Reason, h.Fix = rexec.Classify(h.Server.Name, err, stderr)
		return
	}

	// The delta needs the previous sample, so it is taken before overwriting.
	if h.hasPrev && snap.HasCPU {
		if pct, ok := model.CPUPercent(h.prevCPU, snap.CPU); ok {
			h.CPUPct, h.HasCPU = pct, true
		}
	}
	if snap.HasCPU {
		h.prevCPU, h.hasPrev = snap.CPU, true
	}

	h.Snap, h.HasSnap = snap, true
	h.Status, h.Reason, h.Fix = model.StatusOK, "", ""
	h.LastOK = time.Now()
}

// Summary counts what the footer reports.
func (f *Fleet) Summary() (total, ok, attention int) {
	for _, h := range f.Hosts() {
		total++
		if h.Status == model.StatusOK {
			ok++
		}
		if h.NeedsAttention() {
			attention++
		}
	}
	return
}

// host looks up one server's state.
func (f *Fleet) host(name string) (*Host, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	h, ok := f.hosts[name]
	return h, ok
}

// Server returns one host's identity, for callers that need the address or
// tags without the whole state.
func (f *Fleet) Server(name string) (model.Server, bool) {
	h, ok := f.host(name)
	if !ok {
		return model.Server{}, false
	}
	return h.Server, true
}

// Processes collects one host's process table on demand.
//
// Detail collections are deliberately not folded into the fleet snapshot:
// they answer a question about one machine that somebody is looking at, and
// carrying them for every host on every refresh would cost far more than
// the whole fleet view does.
func (f *Fleet) Processes(ctx context.Context, name string) (model.ProcessList, error) {
	h, ok := f.host(name)
	if !ok {
		return model.ProcessList{}, errUnknownHost
	}
	release, err := f.acquire(ctx)
	if err != nil {
		return model.ProcessList{}, err
	}
	defer release()

	hctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	return probe.RunProcesses(hctx, f.ex, h.Server.Target())
}

// Services collects one host's service list on demand.
func (f *Fleet) Services(ctx context.Context, name string) (model.ServiceList, error) {
	h, ok := f.host(name)
	if !ok {
		return model.ServiceList{}, errUnknownHost
	}
	release, err := f.acquire(ctx)
	if err != nil {
		return model.ServiceList{}, err
	}
	defer release()

	hctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	return probe.RunServices(hctx, f.ex, h.Server.Target())
}

var errUnknownHost = errors.New("unknown host")

// acquire takes a slot from the same bound the fleet refresh uses, so
// opening a detail screen mid-refresh cannot exceed the connection limit.
func (f *Fleet) acquire(ctx context.Context) (func(), error) {
	select {
	case f.sem <- struct{}{}:
		return func() { <-f.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
