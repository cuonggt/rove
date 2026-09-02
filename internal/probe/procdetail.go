package probe

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"

	rexec "github.com/cuonggt/rove/internal/exec"
	"github.com/cuonggt/rove/internal/model"
)

//go:embed procdetail.sh
var ProcDetailScript string

const procHeader = "rove-proc "

// ErrUnsafePID rejects anything that is not a process id.
var ErrUnsafePID = errors.New("unsafe process id")

// ValidatePID guards the same boundary as ValidateUnit and ValidatePath.
// A pid is easy: it is digits or it is refused.
func ValidatePID(pid int) error {
	if pid <= 0 || pid > 1<<22 {
		return fmt.Errorf("%w: %d", ErrUnsafePID, pid)
	}
	return nil
}

// RunProcDetail collects everything known about one process.
func RunProcDetail(ctx context.Context, ex rexec.Executor, t model.Target, pid int) (model.ProcessDetail, error) {
	if err := ValidatePID(pid); err != nil {
		return model.ProcessDetail{}, err
	}
	res, err := ex.Run(ctx, t, rexec.Command{
		Argv:  []string{"sh", "-s", "--", strconv.Itoa(pid)},
		Stdin: bytes.NewReader([]byte(ProcDetailScript)),
	})
	if err != nil {
		return model.ProcessDetail{}, err
	}
	return ParseProcDetail(res.Stdout)
}

// ParseProcDetail reads the process-detail contract.
func ParseProcDetail(out []byte) (model.ProcessDetail, error) {
	var p model.ProcessDetail

	body, err := scanHeader(out, procHeader)
	if err != nil {
		return p, err
	}

	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "proc.pid":
			p.PID = int(atoi64(val))
		case "proc.comm":
			p.Comm = val
		case "proc.cmdline":
			p.Cmdline = val
		case "proc.state":
			p.State = val
		case "proc.state_text":
			p.StateText = val
		case "proc.ppid":
			p.PPid = int(atoi64(val))
		case "proc.parent":
			p.Parent = val
		case "proc.threads":
			p.Threads = int(atoi64(val))
		case "proc.uid":
			p.UID = int(atoi64(val))
		case "proc.user":
			p.User = val
		case "proc.rss_kb":
			p.RSSKB = atoi64(val)
		case "proc.vsz_kb":
			p.VSZKB = atoi64(val)
		case "proc.elapsed_s":
			p.ElapsedS = atoi64(val)
		case "proc.exe":
			p.Exe = val
		case "proc.cwd":
			p.Cwd = val
		case "proc.fds":
			p.FDs, p.HasFDs = int(atoi64(val)), true
		case "proc.container":
			p.Container = val
		case "proc.limited":
			p.Limited = val == "1"
		case "proc.error":
			p.Err = val
		}
	}
	return p, sc.Err()
}
