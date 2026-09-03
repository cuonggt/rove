package action

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

//go:embed act.sh
var Script string

const actHeader = "rove-act "

// ErrNotConfirmed is returned when an action reaches the runner without a
// confirmation that matches it.
var ErrNotConfirmed = errors.New("action was not confirmed")

// Confirmation is proof that a person was shown this exact action, on this
// exact host, and agreed to it.
//
// Its fields are unexported and it carries no usable zero value, so the
// only way to obtain one is to call Confirm. Forgetting to confirm cannot
// be written as a shorter call that silently works: it fails closed.
type Confirmation struct {
	host   string
	kind   Kind
	target string
	given  bool
}

// Confirm records agreement to one specific action on one specific host.
func Confirm(host string, a Action) Confirmation {
	return Confirmation{host: host, kind: a.Kind, target: a.Target, given: true}
}

// matches rejects a confirmation reused for a different action, a different
// target, or a different machine. Agreeing to restart nginx on staging is
// not agreement to restart it on production.
func (c Confirmation) matches(host string, a Action) bool {
	return c.given && c.host == host && c.kind == a.Kind && c.target == a.Target
}

// Result is what happened.
type Result struct {
	Verb   string
	Target string
	// Privilege is root, sudo or none: how the command was run, which is
	// usually the answer when it failed.
	Privilege string
	ExitCode  int
	// State is what the thing looks like now. An action that reports
	// success without saying so leaves the reader to go and check anyway.
	State string
	OK    bool
	Err   string
}

// Runner performs actions on hosts.
type Runner struct {
	ex rexec.Executor
}

func NewRunner(ex rexec.Executor) *Runner { return &Runner{ex: ex} }

// Run performs an action. It requires a Confirmation obtained for this exact
// action and host.
func (r *Runner) Run(ctx context.Context, host string, t model.Target, a Action, c Confirmation) (Result, error) {
	if err := a.Validate(); err != nil {
		return Result{}, err
	}
	if !c.matches(host, a) {
		return Result{}, fmt.Errorf("%w: %s", ErrNotConfirmed, a.Summary(host))
	}

	s, _ := a.spec()
	argv := append([]string{"sh", "-s", "--", s.verb}, strings.Fields(a.Target)...)

	res, err := r.ex.Run(ctx, t, rexec.Command{
		Argv:  argv,
		Stdin: bytes.NewReader([]byte(Script)),
	})
	if err != nil {
		return Result{}, err
	}
	return ParseResult(res.Stdout)
}

// ParseResult reads the action contract.
func ParseResult(out []byte) (Result, error) {
	var r Result

	idx := bytes.IndexByte(out, '\n')
	if idx < 0 {
		return r, fmt.Errorf("no %sheader in output", actHeader)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(out[:idx])), actHeader) {
		return r, fmt.Errorf("no %sheader in output", actHeader)
	}

	sc := bufio.NewScanner(bytes.NewReader(out[idx+1:]))
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "act.verb":
			r.Verb = val
		case "act.target":
			r.Target = val
		case "act.privilege":
			r.Privilege = val
		case "act.exit":
			n, _ := strconv.Atoi(val)
			r.ExitCode = n
		case "act.state":
			r.State = val
		case "act.ok":
			r.OK = val == "1"
		case "act.error":
			r.Err = val
		}
	}
	return r, sc.Err()
}
