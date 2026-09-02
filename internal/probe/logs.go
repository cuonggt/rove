package probe

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	rexec "github.com/cuonggt/rove/internal/exec"
	"github.com/cuonggt/rove/internal/model"
)

//go:embed logs.sh
var LogScript string

const (
	logHeader = "rove-logs "
	// DefaultLogLines is a window, not a log viewer. Enough to see why a
	// unit died, few enough to cross a slow link inside one deadline.
	DefaultLogLines = 200
	maxLogLines     = 2000
)

// ErrUnsafeUnit rejects a unit name that could not have come from systemd.
var ErrUnsafeUnit = errors.New("unsafe unit name")

// safeUnit is deliberately stricter than systemd's own rules.
//
// This name is handed to ssh, which concatenates its arguments into one
// string that the *remote* shell then re-splits and expands. A name
// containing a space, a semicolon or a dollar sign would therefore run as
// code on the host. Unit names arrive from the remote machine's own
// output, which makes them untrusted input no matter how ordinary they
// look, so the check happens here rather than in the script.
var safeUnit = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._:+-]{0,127}$`)

// ValidateUnit reports whether a unit name can be sent to a remote shell.
func ValidateUnit(name string) error {
	if name == "" {
		return nil // the whole system, sent as the "-" sentinel
	}
	if !safeUnit.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrUnsafeUnit, name)
	}
	return nil
}

// RunLogs tails one host's log, optionally scoped to a single unit.
func RunLogs(ctx context.Context, ex rexec.Executor, t model.Target, unit string, lines int) (model.LogTail, error) {
	if err := ValidateUnit(unit); err != nil {
		return model.LogTail{}, err
	}
	if lines <= 0 {
		lines = DefaultLogLines
	}
	if lines > maxLogLines {
		lines = maxLogLines
	}

	arg := unit
	if arg == "" {
		arg = "-"
	}

	res, err := ex.Run(ctx, t, rexec.Command{
		Argv:  []string{"sh", "-s", "--", arg, strconv.Itoa(lines)},
		Stdin: bytes.NewReader([]byte(LogScript)),
	})
	if err != nil {
		return model.LogTail{}, err
	}
	tail, perr := ParseLogs(res.Stdout)
	if perr != nil {
		return model.LogTail{}, perr
	}
	if tail.Unit == "" {
		tail.Unit = unit
	}
	return tail, nil
}

// ParseLogs reads the log contract. Every log line arrives with its own key
// so that a line containing "key=value" cannot be mistaken for one.
func ParseLogs(out []byte) (model.LogTail, error) {
	var l model.LogTail

	body, err := scanHeader(out, logHeader)
	if err != nil {
		return l, err
	}

	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "log.unit":
			l.Unit = val
		case "log.source":
			l.Source = val
		case "log.error":
			l.Err = val
		case "log.limited":
			l.Limited = val == "1"
		case "log.line":
			l.Lines = append(l.Lines, val)
		}
	}
	return l, sc.Err()
}
