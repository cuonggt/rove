package inventory

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Diagnosis is what `rove doctor` reports. Nearly every problem with a tool
// like this is "it does not see my servers", and answering that by
// correspondence is miserable for everyone.
type Diagnosis struct {
	ConfigPath   string
	ConfigExists bool
	SSHVersion   string
	// Multiplexing needs OpenSSH 6.7 for the %C control-path token. Without
	// it every refresh pays a full handshake.
	SupportsControlPathHash bool
	// SupportsG is `ssh -G`, which is how every host is resolved.
	SupportsG  bool
	ControlDir string

	Scanned  []Entry
	Ignored  []string
	Resolved []Resolution
	Warnings []string
}

// Resolution is one alias and what OpenSSH says it means.
type Resolution struct {
	Alias   string
	Address string
	User    string
	Port    string
	Via     string
	Env     string
	Tags    []string
	Err     string
}

// Diagnose gathers everything needed to explain what rove can and cannot
// see, without connecting to a single host.
func Diagnose(ctx context.Context, configPath string) Diagnosis {
	d := Diagnosis{ConfigPath: configPath}

	if _, err := os.Stat(configPath); err == nil {
		d.ConfigExists = true
	} else {
		d.Warnings = append(d.Warnings, "no ssh config at "+configPath)
	}

	d.SSHVersion, d.SupportsG, d.SupportsControlPathHash = sshCapabilities(ctx)
	if !d.SupportsG {
		d.Warnings = append(d.Warnings,
			"this OpenSSH has no -G; hosts fall back to unresolved config values")
	}
	if !d.SupportsControlPathHash {
		d.Warnings = append(d.Warnings,
			"this OpenSSH has no %C control-path token; connection reuse is disabled")
	}

	if home, err := os.UserHomeDir(); err == nil {
		d.ControlDir = filepath.Join(home, ".rove", "cm")
	}

	entries, err := Scan(configPath)
	if err != nil {
		d.Warnings = append(d.Warnings, "reading config: "+err.Error())
	}
	d.Scanned = entries

	for _, e := range entries {
		if e.Ann.Ignore {
			d.Ignored = append(d.Ignored, e.Alias)
			continue
		}
		s := Resolve(ctx, configPath, e)
		r := Resolution{
			Alias:   s.Name,
			Address: s.Address,
			User:    s.User,
			Port:    s.Port,
			Via:     s.Meta.ProxyJump,
			Env:     s.Meta.Env,
			Tags:    s.Meta.Tags,
		}
		// An alias that resolves to itself means -G told us nothing, which
		// usually means the entry is a pattern or the alias is unknown.
		if s.Address == "" || s.Address == s.Name {
			r.Err = "did not resolve to a hostname"
		}
		d.Resolved = append(d.Resolved, r)
	}

	if len(d.Resolved) == 0 && d.ConfigExists {
		d.Warnings = append(d.Warnings,
			"the config has no host entries rove can use (wildcards and Match blocks are skipped)")
	}
	return d
}

// sshCapabilities asks the local ssh what it can do, rather than assuming.
func sshCapabilities(ctx context.Context) (version string, hasG, hasHash bool) {
	out, err := exec.CommandContext(ctx, "ssh", "-V").CombinedOutput()
	if err != nil {
		return "not found", false, false
	}
	version = strings.TrimSpace(string(out))

	// -G against a name that cannot exist: it either prints the resolved
	// config or complains about the flag.
	if err := exec.CommandContext(ctx, "ssh", "-G", "rove-doctor-probe").Run(); err == nil {
		hasG = true
	}
	// %C arrived in 6.7 alongside -G in 6.8, so -G implies it in practice.
	// Checking both keeps the report honest on unusual builds.
	hasHash = hasG || strings.Contains(version, "OpenSSH_6.7")
	return version, hasG, hasHash
}
