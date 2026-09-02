package exec

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cuonggt/rove/internal/model"
)

// SSH runs commands through the user's own OpenSSH client, so that agents,
// IdentityFile, ProxyJump, known_hosts and hardware keys all keep working
// without rove knowing anything about them.
type SSH struct {
	ConnectTimeout time.Duration
	// Multiplex reuses one connection per host across refreshes. It turns a
	// 200-800ms handshake into ~10ms on every probe after the first.
	Multiplex   bool
	ControlPath string
	// ConfigPath overrides the per-user ssh config. Empty means the user's
	// own, which is the default and the only value in normal use.
	ConfigPath string
}

func NewSSH() *SSH {
	home, _ := os.UserHomeDir()
	return &SSH{
		ConnectTimeout: 5 * time.Second,
		Multiplex:      true,
		// %C is a hash of the connection parameters. It is used rather than
		// %h/%p/%r because macOS caps unix socket paths near 104 bytes.
		ControlPath: filepath.Join(home, ".rove", "cm", "%C"),
	}
}

func (s *SSH) args(alias string) []string {
	a := []string{}
	if s.ConfigPath != "" {
		a = append(a, "-F", s.ConfigPath)
	}
	a = append(a,
		// BatchMode is not optional. Without it a host with a locked key or
		// an unknown host key blocks forever on a prompt nobody can see, and
		// the fleet table never settles.
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout="+strconv.Itoa(int(s.ConnectTimeout.Seconds())),
		"-T",
	)
	if s.Multiplex {
		_ = os.MkdirAll(filepath.Dir(s.ControlPath), 0o700)
		a = append(a,
			"-o", "ControlMaster=auto",
			"-o", "ControlPath="+s.ControlPath,
			"-o", "ControlPersist=120",
		)
	}
	return append(a, alias)
}

func (s *SSH) Run(ctx context.Context, t model.Target, c Command) (Result, error) {
	args := append(s.args(t.Alias), c.Argv...)

	cmd := exec.Command("ssh", args...)
	if c.Stdin != nil {
		cmd.Stdin = c.Stdin
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	setPgid(cmd)

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{}, err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-ctx.Done():
		// Kill the whole group: ssh with a ProxyCommand spawns children that
		// outlive a plain Process.Kill and would otherwise leak per timeout.
		killGroup(cmd)
		<-done
		return Result{Duration: time.Since(start)}, ctx.Err()
	case err := <-done:
		r := Result{
			Stdout:   stdout.Bytes(),
			Stderr:   stderr.Bytes(),
			Duration: time.Since(start),
		}
		if ee, ok := err.(*exec.ExitError); ok {
			r.ExitCode = ee.ExitCode()
			// 255 is how ssh itself reports a transport failure; anything
			// else is the remote command's own exit status.
			if r.ExitCode == 255 {
				return r, &TransportError{Err: err, Stderr: string(r.Stderr)}
			}
			return r, nil
		}
		return r, err
	}
}

var _ Executor = (*SSH)(nil)
