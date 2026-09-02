// Package probe collects a read-only snapshot of a remote host in a single
// round trip, and parses what comes back.
package probe

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"time"

	rexec "github.com/cuonggt/rove/internal/exec"
	"github.com/cuonggt/rove/internal/model"
)

// Script is fed to `sh -s` on stdin rather than passed as an argument, which
// avoids quoting entirely and keeps the probe a real file that can be linted,
// tested and run by hand.
//
//go:embed probe.sh
var Script string

// ContractVersion is the version this client understands. The parser accepts
// any probe whose header is this or newer, and ignores keys it does not know,
// so a host running a newer probe degrades to fewer fields rather than an
// error.
const ContractVersion = 1

// Run probes one host.
func Run(ctx context.Context, ex rexec.Executor, t model.Target) (model.Snapshot, error) {
	res, err := ex.Run(ctx, t, rexec.Command{
		Argv:  []string{"sh", "-s"},
		Stdin: bytes.NewReader([]byte(Script)),
	})
	if err != nil {
		return model.Snapshot{}, err
	}
	snap, perr := Parse(res.Stdout)
	if perr != nil {
		// A host that connects but returns nothing usable is not healthy,
		// however cleanly ssh exited. Restricted shells, forced commands and
		// gateways that accept a connection with nothing behind it all land
		// here, and all of them would otherwise read as a working server.
		if res.ExitCode != 0 {
			return model.Snapshot{}, fmt.Errorf("%w (remote exit %d)", perr, res.ExitCode)
		}
		return model.Snapshot{}, perr
	}
	snap.At = time.Now()
	return snap, nil
}
