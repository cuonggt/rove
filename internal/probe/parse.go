package probe

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cuonggt/rove/internal/model"
)

// ErrNoHeader means the output did not come from a rove probe at all.
var ErrNoHeader = errors.New("no rove-probe header in output")

const headerPrefix = "rove-probe "

// Parse turns probe output into a Snapshot. Unknown keys are ignored so that
// a host running a newer probe stays readable, and repeated keys accumulate.
func Parse(out []byte) (model.Snapshot, error) {
	var s model.Snapshot

	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	if !sc.Scan() {
		return s, ErrNoHeader
	}
	header := strings.TrimSpace(sc.Text())
	if !strings.HasPrefix(header, headerPrefix) {
		return s, ErrNoHeader
	}
	v, err := strconv.Atoi(strings.TrimSpace(header[len(headerPrefix):]))
	if err != nil {
		return s, fmt.Errorf("unreadable probe version %q", header)
	}
	if v < ContractVersion {
		return s, fmt.Errorf("probe contract v%d is older than v%d", v, ContractVersion)
	}

	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "sys.kind":
			s.Kind = val
		case "sys.os":
			s.OS = val
		case "sys.kernel":
			s.Kernel = val
		case "sys.arch":
			s.Arch = val
		case "sys.uptime_s":
			s.UptimeS = atoi64(val)
		case "cpu.cores":
			s.Cores = int(atoi64(val))
		case "cpu.stat":
			if c, ok := parseCPUStat(val); ok {
				s.CPU, s.HasCPU = c, true
			}
		case "load":
			if l, ok := parseLoad(val); ok {
				s.Load, s.HasLoad = l, true
			}
		case "mem.total_kb":
			s.MemTotalKB = atoi64(val)
		case "mem.available_kb":
			s.MemAvailKB = atoi64(val)
		case "swap.total_kb":
			s.SwapTotalKB = atoi64(val)
		case "swap.free_kb":
			s.SwapFreeKB = atoi64(val)
		case "fs":
			if f, ok := parseFS(val); ok {
				s.Filesystems = append(s.Filesystems, f)
			}
		case "net":
			if name, addr, ok := strings.Cut(val, " "); ok {
				s.Interfaces = append(s.Interfaces, model.Iface{Name: name, Addr: addr})
			}
		case "svc.init":
			s.Init = val
		case "svc.state":
			s.InitState = val
		case "svc.query":
			s.SvcQuery = val
		case "svc.failed":
			s.FailedUnits = append(s.FailedUnits, val)
		case "probe.ms":
			s.ProbeMS = atoi64(val)
		}
	}
	return s, sc.Err()
}

// parseCPUStat reads the first line of /proc/stat. The kernel pads the label
// with two spaces, so fields are split on whitespace rather than by index.
func parseCPUStat(v string) (model.CPUStat, bool) {
	f := strings.Fields(v)
	if len(f) < 5 || f[0] != "cpu" {
		return model.CPUStat{}, false
	}
	n := make([]uint64, 8)
	for i := 0; i < 8 && i+1 < len(f); i++ {
		n[i], _ = strconv.ParseUint(f[i+1], 10, 64)
	}
	return model.CPUStat{
		User: n[0], Nice: n[1], System: n[2], Idle: n[3],
		IOWait: n[4], IRQ: n[5], SoftIRQ: n[6], Steal: n[7],
	}, true
}

func parseLoad(v string) ([3]float64, bool) {
	f := strings.Fields(v)
	if len(f) < 3 {
		return [3]float64{}, false
	}
	var out [3]float64
	for i := 0; i < 3; i++ {
		n, err := strconv.ParseFloat(f[i], 64)
		if err != nil {
			return [3]float64{}, false
		}
		out[i] = n
	}
	return out, true
}

// parseFS reads "device total_kb used_kb mount". The mount point comes last
// because it is the field allowed to contain spaces.
func parseFS(v string) (model.Filesystem, bool) {
	f := strings.SplitN(v, " ", 4)
	if len(f) < 4 {
		return model.Filesystem{}, false
	}
	total, err1 := strconv.ParseInt(f[1], 10, 64)
	used, err2 := strconv.ParseInt(f[2], 10, 64)
	if err1 != nil || err2 != nil {
		return model.Filesystem{}, false
	}
	return model.Filesystem{
		Device: f[0], TotalKB: total, UsedKB: used, Mount: f[3],
	}, true
}

func atoi64(v string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	return n
}
