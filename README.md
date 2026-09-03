# rove

A terminal console for the Linux servers you already reach over SSH.

If `ssh server` works, rove works. It reads `~/.ssh/config` and never asks for
a private key.

![rove](docs/demo.gif)

## Install

macOS:

```
brew install --cask cuonggt/tap/rove
```

Linux, or anywhere with Go:

```
go install github.com/cuonggt/rove/cmd/rove@latest
```

Or take a static binary from [releases](https://github.com/cuonggt/rove/releases).

There is nothing to configure. Run `rove`.

## Scope

rove is deliberately narrow. It covers servers reachable from your ssh config,
and its roadmap ends at fleet operations:

| Version | Promise |
| --- | --- |
| v0.1 | See every server without SSH'ing into them one by one |
| v0.2 | Find what's wrong without remembering Linux commands |
| v0.3 | Operate the fleet without opening five SSH sessions |
| v0.4 | One command, many machines, one result table |

**Cloud provider discovery is out of scope, permanently.** AWS, DigitalOcean
and Hetzner inventory belong to [CloudDesk](../clouddesk); rove will read that
inventory as a second source rather than growing provider adapters of its own.

## Status

v0.1: discovery, the probe, the interface, and the
processes, services and disk screens.

```
go build -o rove ./cmd/rove
./rove           # the interface
./rove -list     # resolved inventory, no network
./rove -once     # print the fleet once and exit
./rove doctor    # explain what rove can see, without connecting
```

`-once` is chosen automatically when stdout is not a terminal, so piping or
redirecting works without a flag.

| Key | |
| --- | --- |
| `↑` `↓` / `j` `k` | move |
| `/` | filter by name, environment or tag |
| `⏎` | open the overview card |
| `s` | drop into `ssh`, and return here on exit |
| `r` | refresh now |
| `q` | quit |

Inside a host: `o` overview, `p` processes, `v` services, `d` disk, `esc`
back to the fleet.

CPU is a delta between two samples, so it reads `—` until the second
refresh lands. `-once` never gets one; the interface does.

### Testing

Unit tests are offline and fast. Integration runs against real sshd on five
distributions:

```
test/fixtures/sshd/up.sh
go test -tags=integration ./test/...
test/fixtures/sshd/down.sh
```

One fixture runs real systemd as PID 1 and ships a unit that always fails,
so the failed-unit path is covered end to end rather than by assumption.

## Releasing

Tag and push; CI builds every platform and updates the Homebrew tap.

```
git tag -a v0.1.0 -m "v0.1.0" && git push origin v0.1.0
```

Binaries are static (`CGO_ENABLED=0`, no cgo anywhere) for darwin and linux
on amd64 and arm64, so one linux build runs on both glibc and musl.

The recording is regenerated with [vhs](https://github.com/charmbracelet/vhs):

```
test/fixtures/sshd/up.sh && docs/demo-setup.sh && vhs docs/demo.tape
```

## Design rules

- **Native OpenSSH, always.** Agents, `IdentityFile`, `ProxyJump`,
  `known_hosts` and hardware keys keep working because rove shells out to the
  user's own client rather than reimplementing SSH.
- **`BatchMode=yes` on every probe.** A host that would prompt fails fast into
  a legible state instead of hanging a goroutine forever.
- **Reading never writes.** Every script in `internal/probe` is read-only and
  needs no sudo, and `TestProbeScriptIsReadOnly` enforces that by parsing
  them. Everything that changes a host lives in `internal/action`, which
  `internal/probe` is forbidden to import -- also by test. The guarantee is
  structural rather than a habit.
- **Nothing changes without agreement.** An action reaches a host only with a
  `Confirmation` naming that exact verb, target and machine. The zero value
  never matches, so a forgotten confirmation fails closed rather than
  running quietly. Agreeing to restart nginx on staging is not agreement to
  restart it on production.
- **Dangerous actions ask for a word, not a keystroke.** Stopping a service,
  killing a process and stopping a container all require typing `yes`,
  because `y` is muscle memory. The prompt names the machine and the
  specific consequence; "are you sure" teaches people to press y without
  reading. Signalling pid 1 is never offered at all.
- **Absence is never evidence.** A missing figure is `—`, not zero. An init
  system that is installed but unqueryable is reported as unreadable rather
  than as a host with no failures, and a capped list says what it left out.
- **One round trip per host per refresh.** The probe is a single POSIX shell
  script piped to `sh -s`, not a command per metric. Percentages are computed
  from raw counters on the client, so no host ever sleeps to be measured.
  Process and service tables are collected on demand for the one host you
  are looking at, never for the whole fleet.
- **Every failure names its fix.** A bare "unreachable" is not actionable.
