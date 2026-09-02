package probe

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"sort"
	"strconv"
	"strings"

	rexec "github.com/cuonggt/rove/internal/exec"
	"github.com/cuonggt/rove/internal/model"
)

// Detail probes run for one host, on demand, when a screen that needs them
// is opened. They are deliberately not part of the fleet snapshot.
//
//go:embed processes.sh
var ProcessScript string

//go:embed services.sh
var ServiceScript string

const (
	processHeader = "rove-processes "
	serviceHeader = "rove-services "
	// processLimit mirrors the cap in processes.sh. The two must agree or
	// the interface will claim a list is complete when it was truncated.
	processLimit = 60
)

// RunProcesses collects the process table from one host.
func RunProcesses(ctx context.Context, ex rexec.Executor, t model.Target) (model.ProcessList, error) {
	out, err := runScript(ctx, ex, t, ProcessScript)
	if err != nil {
		return model.ProcessList{}, err
	}
	return ParseProcesses(out)
}

// RunServices collects the service list from one host.
func RunServices(ctx context.Context, ex rexec.Executor, t model.Target) (model.ServiceList, error) {
	out, err := runScript(ctx, ex, t, ServiceScript)
	if err != nil {
		return model.ServiceList{}, err
	}
	return ParseServices(out)
}

func runScript(ctx context.Context, ex rexec.Executor, t model.Target, script string) ([]byte, error) {
	res, err := ex.Run(ctx, t, rexec.Command{
		Argv:  []string{"sh", "-s"},
		Stdin: bytes.NewReader([]byte(script)),
	})
	if err != nil {
		return nil, err
	}
	return res.Stdout, nil
}

// ParseProcesses reads the process contract. Rows whose first field is not a
// PID are dropped, which quietly handles the busybox ps that insists on
// printing a header.
func ParseProcesses(out []byte) (model.ProcessList, error) {
	var p model.ProcessList

	body, err := scanHeader(out, processHeader)
	if err != nil {
		return p, err
	}

	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "proc.fields":
			p.Fields = strings.Split(val, ",")
		case "proc.sorted":
			p.SortedRemotely = val == "cpu"
		case "proc.total":
			p.Total = int(atoi64(val))
		case "proc.error":
			p.Err = val
		case "proc":
			if proc, ok := parseProcess(val, p.Fields); ok {
				p.Procs = append(p.Procs, proc)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return p, err
	}

	if p.Total > len(p.Procs) {
		p.Truncated = true
	}
	if p.Total == 0 {
		p.Total = len(p.Procs)
	}
	// A host that could not sort leaves it to us. Sorting a truncated list
	// does not recover the rows the host already dropped, which is why
	// SortedRemotely is reported rather than hidden.
	if !p.SortedRemotely {
		sort.SliceStable(p.Procs, func(i, j int) bool {
			return p.Procs[i].CPU > p.Procs[j].CPU
		})
	}
	return p, nil
}

// parseProcess reads one row. The command is always last because it is the
// field allowed to contain spaces.
func parseProcess(line string, fields []string) (model.Process, bool) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return model.Process{}, false
	}
	pid, err := strconv.Atoi(f[0])
	if err != nil {
		return model.Process{}, false // a header row, or noise
	}

	p := model.Process{PID: pid}
	hasPercents := len(fields) >= 4 && fields[2] == "cpu"

	switch {
	case hasPercents && len(f) >= 6:
		p.User = f[1]
		if v, err := strconv.ParseFloat(f[2], 64); err == nil {
			p.CPU, p.HasCPU = v, true
		}
		if v, err := strconv.ParseFloat(f[3], 64); err == nil {
			p.Mem, p.HasMem = v, true
		}
		if v, err := strconv.ParseInt(f[4], 10, 64); err == nil {
			p.RSSKB, p.HasRSS = v, true
		}
		p.Command = strings.Join(f[5:], " ")
	case len(f) >= 4:
		// busybox: pid, user, vsz, comm
		p.User = f[1]
		if v, err := strconv.ParseInt(strings.TrimSuffix(f[2], "m"), 10, 64); err == nil {
			p.RSSKB, p.HasRSS = v, true
		}
		p.Command = strings.Join(f[3:], " ")
	case len(f) >= 2:
		p.User = f[1]
	default:
		return model.Process{}, false
	}

	if p.Command == "" {
		return model.Process{}, false
	}
	return p, true
}

// ParseServices reads the service contract.
func ParseServices(out []byte) (model.ServiceList, error) {
	var s model.ServiceList

	body, err := scanHeader(out, serviceHeader)
	if err != nil {
		return s, err
	}

	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "svc.init":
			s.Init = val
		case "svc.state":
			s.State = val
		case "unit":
			if u, ok := parseUnit(val); ok {
				s.Units = append(s.Units, u)
			}
		}
	}
	return s, sc.Err()
}

// parseUnit reads "name load active sub description...".
func parseUnit(val string) (model.Unit, bool) {
	f := strings.SplitN(val, " ", 5)
	if len(f) < 4 || f[0] == "" {
		return model.Unit{}, false
	}
	u := model.Unit{Name: f[0], Load: f[1], Active: f[2], Sub: f[3]}
	if len(f) == 5 {
		u.Description = strings.TrimSpace(f[4])
	}
	return u, true
}

// scanHeader verifies the contract line and returns everything after it. A
// host that answers without one did not run our script, whatever its exit
// status claimed.
func scanHeader(out []byte, prefix string) ([]byte, error) {
	idx := bytes.IndexByte(out, '\n')
	if idx < 0 {
		if len(bytes.TrimSpace(out)) == 0 {
			return nil, ErrNoHeader
		}
		idx = len(out)
	}
	header := strings.TrimSpace(string(out[:idx]))
	if !strings.HasPrefix(header, prefix) {
		return nil, ErrNoHeader
	}
	v, err := strconv.Atoi(strings.TrimSpace(header[len(prefix):]))
	if err != nil || v < ContractVersion {
		return nil, ErrNoHeader
	}
	if idx >= len(out) {
		return nil, nil
	}
	return out[idx+1:], nil
}
