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

//go:embed du.sh
var DuScript string

const duHeader = "rove-du "

// ErrUnsafePath rejects a path that could not have come from a mount table.
var ErrUnsafePath = errors.New("unsafe path")

// ValidatePath guards the same boundary as ValidateUnit: the path is handed
// to ssh, which concatenates argv into a string the remote shell re-splits
// and expands. Mount points arrive from the remote host's own df output and
// are therefore untrusted.
//
// Spaces are allowed because a mount point may legitimately contain one;
// the script reassembles them from "$*". Everything a shell would act on is
// not.
func ValidatePath(p string) error {
	if p == "" || !strings.HasPrefix(p, "/") {
		return fmt.Errorf("%w: must be absolute: %q", ErrUnsafePath, p)
	}
	if len(p) > 4096 {
		return fmt.Errorf("%w: too long", ErrUnsafePath)
	}
	if strings.ContainsAny(p, "\n\r\t;|&$`<>(){}[]*?!\\\"'") {
		return fmt.Errorf("%w: %q", ErrUnsafePath, p)
	}
	return nil
}

// RunDu asks what is filling up one path.
func RunDu(ctx context.Context, ex rexec.Executor, t model.Target, path string) (model.DirUsage, error) {
	if err := ValidatePath(path); err != nil {
		return model.DirUsage{}, err
	}
	// The path is split into words deliberately: the script rejoins them
	// with "$*", which is what makes a mount point with a space survive.
	argv := append([]string{"sh", "-s", "--"}, strings.Fields(path)...)

	res, err := ex.Run(ctx, t, rexec.Command{
		Argv:  argv,
		Stdin: bytes.NewReader([]byte(DuScript)),
	})
	if err != nil {
		return model.DirUsage{}, err
	}
	return ParseDu(res.Stdout)
}

// ParseDu reads the directory-size contract.
func ParseDu(out []byte) (model.DirUsage, error) {
	var d model.DirUsage

	body, err := scanHeader(out, duHeader)
	if err != nil {
		return d, err
	}

	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "du.path":
			d.Path = val
		case "du.unreadable":
			d.Unreadable = int(atoi64(val))
		case "du.timedout":
			d.TimedOut = val == "1"
		case "du.shallow":
			d.Shallow = val == "1"
		case "du.error":
			d.Err = val
		case "entry":
			// du writes "<kb>\t<path>"; the tab is what keeps a path with
			// spaces intact.
			size, path, ok := strings.Cut(val, "\t")
			if !ok {
				continue
			}
			kb, err := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
			if err != nil {
				continue
			}
			d.Entries = append(d.Entries, model.DirEntry{Path: path, KB: kb})
		}
	}
	return d, sc.Err()
}
