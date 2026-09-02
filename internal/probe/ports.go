package probe

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"strconv"
	"strings"

	rexec "github.com/cuonggt/rove/internal/exec"
	"github.com/cuonggt/rove/internal/model"
)

//go:embed ports.sh
var PortScript string

const portHeader = "rove-ports "

// RunPorts collects what one host is listening on.
func RunPorts(ctx context.Context, ex rexec.Executor, t model.Target) (model.PortList, error) {
	out, err := runScript(ctx, ex, t, PortScript)
	if err != nil {
		return model.PortList{}, err
	}
	return ParsePorts(out)
}

// ParsePorts reads the ports contract.
func ParsePorts(out []byte) (model.PortList, error) {
	var p model.PortList

	body, err := scanHeader(out, portHeader)
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
		case "port.source":
			p.Source = val
		case "port.limited":
			p.Limited = val == "1"
		case "port.error":
			p.Err = val
		case "listen":
			if l, ok := parseListener(val); ok {
				p.Listeners = append(p.Listeners, l)
			}
		case "conn":
			if state, count, ok := strings.Cut(val, " "); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(count)); err == nil {
					p.Conns = append(p.Conns, model.ConnState{State: state, Count: n})
				}
			}
		}
	}
	return p, sc.Err()
}

// parseListener reads "proto addr port pid process". The process name is
// last because it is the field that may contain spaces.
func parseListener(val string) (model.Listener, bool) {
	f := strings.SplitN(val, " ", 5)
	if len(f) < 5 {
		return model.Listener{}, false
	}
	port, err := strconv.Atoi(f[2])
	if err != nil {
		return model.Listener{}, false
	}
	l := model.Listener{Proto: f[0], Addr: f[1], Port: port}

	// "-" is how the script says the owner could not be read, which is not
	// the same as a process literally named "-".
	if f[4] != "-" && f[4] != "" {
		l.Process, l.HasProcess = f[4], true
	}
	if pid, err := strconv.Atoi(f[3]); err == nil {
		l.PID = pid
	}
	return l, true
}
