// Command rove is a terminal console for the Linux servers you already
// reach over SSH.
//
// M0: discovery, resolution and a parallel reachability check. No TUI yet.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	rexec "github.com/cuonggt/rove/internal/exec"
	"github.com/cuonggt/rove/internal/fleet"
	"github.com/cuonggt/rove/internal/inventory"
	"github.com/cuonggt/rove/internal/model"
	"github.com/cuonggt/rove/internal/tui"
)

// Set by the linker at release time. A binary that cannot say which build
// it is turns every bug report into a guessing game.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func buildInfo() string {
	out := "rove " + version
	if commit != "" {
		if len(commit) > 7 {
			commit = commit[:7]
		}
		out += " (" + commit
		if date != "" {
			out += ", " + date
		}
		out += ")"
	}
	return out + "\n" + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH
}

func main() {
	var (
		configPath  = flag.String("config", inventory.DefaultConfigPath(), "ssh config to read")
		listOnly    = flag.Bool("list", false, "show discovered hosts without connecting")
		once        = flag.Bool("once", false, "print the fleet once and exit, instead of the interface")
		timeout     = flag.Duration("timeout", 10*time.Second, "per-host deadline")
		interval    = flag.Duration("interval", 30*time.Second, "time between automatic refreshes")
		concurrency = flag.Int("concurrency", 24, "maximum hosts probed at once")
		noMultiplex = flag.Bool("no-multiplex", false, "disable ssh connection reuse")
		ascii       = flag.Bool("ascii", false, "use plain characters instead of box drawing")
		showVersion = flag.Bool("version", false, "print the build and exit")
	)
	flag.Usage = usage

	// The subcommand is stripped before parsing: Go's flag package stops at
	// the first non-flag argument, so `rove doctor -config X` would
	// otherwise parse no flags at all and silently use the defaults.
	args := os.Args[1:]
	doctor := false
	if len(args) > 0 && args[0] == "doctor" {
		doctor, args = true, args[1:]
	}
	if err := flag.CommandLine.Parse(args); err != nil {
		os.Exit(2)
	}

	if *showVersion {
		fmt.Println(buildInfo())
		return
	}

	ctx := context.Background()

	if doctor {
		printDoctor(inventory.Diagnose(ctx, *configPath))
		return
	}

	servers, err := inventory.Load(ctx, *configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rove:", err)
		os.Exit(1)
	}
	if len(servers) == 0 {
		fmt.Fprintf(os.Stderr, "rove: no hosts found in %s\n", *configPath)
		os.Exit(1)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })

	if *listOnly {
		printInventory(servers)
		return
	}

	ssh := rexec.NewSSH()
	ssh.Multiplex = !*noMultiplex
	if *configPath != inventory.DefaultConfigPath() {
		ssh.ConfigPath = *configPath
	}

	// A hardware key serialises agent signatures and may prompt for a touch
	// per connection, so a wide fan-out becomes a wall of prompts.
	limit := *concurrency
	if anyHardwareKey(servers) && limit > 4 {
		limit = 4
		fmt.Fprintln(os.Stderr, "rove: hardware key detected, limiting to 4 concurrent connections")
	}

	f := fleet.New(ssh, servers, limit, *timeout)

	// Without a terminal there is nothing to drive an interface, so a piped
	// or redirected run prints the table once rather than failing.
	if *once || !isatty.IsTerminal(os.Stdout.Fd()) {
		start := time.Now()
		f.RefreshAll(ctx)
		printFleet(f, time.Since(start))
		return
	}

	opts := tui.Options{Interval: *interval, ASCII: *ascii}
	if *configPath != inventory.DefaultConfigPath() {
		opts.ConfigPath = *configPath
	}
	p := tea.NewProgram(tui.New(f, opts), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "rove:", err)
		os.Exit(1)
	}
}

func anyHardwareKey(servers []model.Server) bool {
	for _, s := range servers {
		if s.Meta.HardwareKey {
			return true
		}
	}
	return false
}

func usage() {
	fmt.Fprint(os.Stderr, `rove - a terminal console for the Linux servers you already reach over ssh

usage:
  rove [flags]           the interface
  rove doctor [flags]    explain what rove can see, without connecting
  rove -version          print the build

flags:
`)
	flag.PrintDefaults()
}

// printDoctor answers "why does rove not see my servers", which is almost
// every question anyone will ever ask about this tool.
func printDoctor(d inventory.Diagnosis) {
	fmt.Println("ssh")
	fmt.Printf("  client        %s\n", d.SSHVersion)
	fmt.Printf("  -G resolve    %s\n", yesNo(d.SupportsG))
	fmt.Printf("  reuse (%%C)    %s\n", yesNo(d.SupportsControlPathHash))
	if d.ControlDir != "" {
		fmt.Printf("  sockets       %s\n", d.ControlDir)
	}

	fmt.Println("\nconfig")
	fmt.Printf("  path          %s%s\n", d.ConfigPath, missingSuffix(d.ConfigExists))
	fmt.Printf("  aliases       %d found, %d ignored\n", len(d.Scanned), len(d.Ignored))
	if len(d.Ignored) > 0 {
		fmt.Printf("  ignored       %s\n", strings.Join(d.Ignored, ", "))
	}

	if len(d.Resolved) > 0 {
		fmt.Println("\nhosts")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  HOST\tENV\tADDRESS\tUSER\tPORT\tVIA\tTAGS\tNOTE")
		for _, r := range d.Resolved {
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				r.Alias, dash(r.Env), dash(r.Address), dash(r.User), dash(r.Port),
				dash(r.Via), dash(strings.Join(r.Tags, ",")), r.Err)
		}
		w.Flush()
	}

	if len(d.Warnings) > 0 {
		fmt.Println("\nwarnings")
		for _, warn := range d.Warnings {
			fmt.Printf("  %s\n", warn)
		}
	}

	fmt.Println("\nrove connects to nothing during doctor. To test one host:")
	fmt.Println("  ssh <host> uname -a")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func missingSuffix(exists bool) string {
	if exists {
		return ""
	}
	return "   (missing)"
}

func printInventory(servers []model.Server) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "HOST\tENV\tADDRESS\tUSER\tPORT\tVIA")
	for _, s := range servers {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Name, dash(s.Meta.Env), dash(s.Address), dash(s.User), dash(s.Port), dash(s.Meta.ProxyJump))
	}
	w.Flush()
	fmt.Printf("\n%d hosts\n", len(servers))
}

func printFleet(f *fleet.Fleet, elapsed time.Duration) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\tHOST\tENV\tCPU\tMEM\tDISK\tLOAD\tNOTE")

	for _, h := range f.Hosts() {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			mark(h), h.Server.Name, dash(h.Server.Meta.Env),
			cpuCol(h), memCol(h), diskCol(h), loadCol(h), h.Note())
	}
	w.Flush()

	total, ok, attention := f.Summary()
	fmt.Printf("\n%d hosts · %d reachable · %d need attention · %dms\n",
		total, ok, attention, elapsed.Milliseconds())

	// CPU is a delta between two samples, so a single pass has nothing to
	// compare against. It fills in from the second refresh onward.
	fmt.Println("cpu needs a second sample; load average is live")

	var fixes []string
	for _, h := range f.Hosts() {
		if h.Fix != "" {
			fixes = append(fixes, fmt.Sprintf("  %-14s %s", h.Server.Name, h.Fix))
		}
	}
	if len(fixes) > 0 {
		fmt.Println("\nto fix:")
		fmt.Println(strings.Join(fixes, "\n"))
	}
}

func mark(h fleet.Host) string {
	switch {
	case h.Status != model.StatusOK:
		return "x"
	case h.Note() != "":
		return "!"
	default:
		return "*"
	}
}

func cpuCol(h fleet.Host) string {
	if !h.HasCPU {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", h.CPUPct)
}

func memCol(h fleet.Host) string {
	pct, ok := h.Snap.MemUsedPercent()
	if !ok {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", pct)
}

func diskCol(h fleet.Host) string {
	fs, ok := h.Snap.FullestFilesystem()
	if !ok {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", fs.UsedPercent())
}

func loadCol(h fleet.Host) string {
	if !h.Snap.HasLoad {
		return "-"
	}
	return fmt.Sprintf("%.2f", h.Snap.Load[0])
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
