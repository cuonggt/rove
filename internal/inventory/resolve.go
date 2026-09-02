package inventory

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cuonggt/rove/internal/model"
)

// Resolve asks OpenSSH what a given alias actually means. `ssh -G` costs a
// fork and no network, and it can never drift from real connection
// behaviour the way a reimplemented parser would.
func Resolve(ctx context.Context, configPath string, e Entry) model.Server {
	s := model.Server{
		Name:   e.Alias,
		Source: model.SourceSSHConfig,
		Conn:   model.ConnSSH,
		Meta: model.Meta{
			Env:  e.Ann.Env,
			Tags: e.Ann.Tags,
		},
	}

	args := []string{}
	if configPath != "" {
		// Without this, -config would change which hosts are discovered but
		// not how they resolve, and every field would silently come from
		// the user's own config instead.
		args = append(args, "-F", configPath)
	}
	args = append(args, "-G", e.Alias)

	out, err := exec.CommandContext(ctx, "ssh", args...).Output()
	if err != nil {
		// -G is unavailable below OpenSSH 6.8. The alias is still usable;
		// we just show less about it.
		s.Address = e.Alias
		return s
	}

	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		key, val, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "hostname":
			s.Address = val
		case "user":
			s.User = val
		case "port":
			s.Port = val
		case "proxyjump":
			if val != "none" {
				s.Meta.ProxyJump = val
			}
		case "identityfile":
			s.Meta.IdentityFiles = append(s.Meta.IdentityFiles, val)
			if isHardwareKey(val) {
				s.Meta.HardwareKey = true
			}
		case "pkcs11provider", "securitykeyprovider":
			if isConfiguredProvider(val) {
				s.Meta.HardwareKey = true
			}
		}
	}

	if s.Meta.Env == "" {
		s.Meta.Env = inferEnv(e.Alias)
	}
	return s
}

// isHardwareKey spots FIDO key types. They serialise signatures through the
// agent and may prompt for a touch per connection, so the caller lowers
// concurrency rather than firing 24 prompts at once.
//
// The file must exist: `ssh -G` reports the whole default IdentityFile list,
// including id_ecdsa_sk and id_ed25519_sk, on hosts that have never seen a
// security key. Matching on the name alone marks every host as hardware.
func isHardwareKey(path string) bool {
	p := strings.ToLower(filepath.Base(path))
	if !strings.HasSuffix(p, "_sk") && !strings.HasSuffix(p, "-sk") {
		return false
	}
	full := path
	if strings.HasPrefix(full, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		full = filepath.Join(home, full[2:])
	}
	_, err := os.Stat(full)
	return err == nil
}

// isConfiguredProvider reports whether a *Provider value names a real
// external provider library.
//
// `ssh -G` prints defaults, not just what the user configured, and those
// defaults differ by version: OpenSSH 9 prints the unexpanded literal
// "$SSH_SK_PROVIDER", OpenSSH 10 prints "internal" for its own built-in
// FIDO support. Both mean "no external provider". Rather than chase each
// new sentinel, this asks the only question with a stable answer: a real
// provider is a path to a shared library.
//
// Getting this wrong is not cosmetic. A false positive drops the whole
// fleet to four concurrent connections to avoid a wall of touch prompts
// that were never going to happen.
func isConfiguredProvider(val string) bool {
	return strings.HasPrefix(val, "/")
}

// inferEnv is the last resort before showing nothing. It only accepts an
// unambiguous prefix, because a wrong environment label is worse than none.
func inferEnv(alias string) string {
	head := strings.ToLower(alias)
	if i := strings.IndexAny(head, "-._"); i > 0 {
		head = head[:i]
	}
	switch head {
	case "prod", "production":
		return "prod"
	case "stg", "staging":
		return "staging"
	case "dev", "development":
		return "dev"
	case "test", "qa":
		return head
	}
	return ""
}

// Load scans a config and resolves every alias it finds, concurrently.
func Load(ctx context.Context, path string) ([]model.Server, error) {
	entries, err := Scan(path)
	if err != nil {
		return nil, err
	}

	servers := make([]model.Server, len(entries))
	keep := make([]bool, len(entries))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)

	for i, e := range entries {
		if e.Ann.Ignore {
			continue
		}
		wg.Add(1)
		go func(i int, e Entry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			servers[i] = Resolve(ctx, path, e)
			keep[i] = true
		}(i, e)
	}
	wg.Wait()

	out := make([]model.Server, 0, len(entries))
	for i := range servers {
		if keep[i] {
			out = append(out, servers[i])
		}
	}
	return out, nil
}
