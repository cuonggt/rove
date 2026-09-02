// Package inventory discovers servers. It deliberately does not implement
// ssh_config semantics: it scans for the literal aliases a user has named,
// and hands each one to `ssh -G` for resolution. That keeps Match blocks,
// canonicalisation, token expansion and per-user defaults as OpenSSH's
// problem rather than ours.
package inventory

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Annotation is rove metadata carried in an ssh_config comment. OpenSSH
// ignores comments, so annotating costs the user nothing and breaks nothing:
//
//	# rove: env=prod tags=web,edge
//	Host prod-web-01
//
//	# rove: ignore
//	Host bastion
type Annotation struct {
	Env    string
	Tags   []string
	Ignore bool
}

// Entry is one alias found in the config, before resolution.
type Entry struct {
	Alias string
	Ann   Annotation
}

const maxIncludeDepth = 8

// Scan returns the literal host aliases declared in path and everything it
// Includes, in declaration order and de-duplicated.
func Scan(path string) ([]Entry, error) {
	seen := map[string]bool{}
	var out []Entry
	err := scanFile(path, 0, map[string]bool{}, func(e Entry) {
		if seen[e.Alias] {
			return
		}
		seen[e.Alias] = true
		out = append(out, e)
	})
	if err != nil && os.IsNotExist(err) {
		return nil, nil // no config is an empty fleet, not a failure
	}
	return out, err
}

func scanFile(path string, depth int, visited map[string]bool, emit func(Entry)) error {
	if depth > maxIncludeDepth {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if visited[abs] {
		return nil // Include cycles are legal to write and fatal to follow
	}
	visited[abs] = true

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var pending Annotation
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if ann, ok := parseAnnotation(line); ok {
				pending = ann
			}
			continue
		}

		key, rest := splitKeyword(line)
		switch strings.ToLower(key) {
		case "include":
			for _, inc := range expandInclude(rest) {
				_ = scanFile(inc, depth+1, visited, emit)
			}
		case "host":
			for _, alias := range strings.Fields(rest) {
				if isPattern(alias) {
					continue
				}
				emit(Entry{Alias: alias, Ann: pending})
			}
			pending = Annotation{}
		case "match":
			// Match blocks apply rules conditionally; they never name a host.
			pending = Annotation{}
		}
	}
	return sc.Err()
}

// splitKeyword handles both `Host x` and the `Host=x` form ssh accepts.
func splitKeyword(line string) (string, string) {
	if i := strings.IndexAny(line, " \t="); i >= 0 {
		return line[:i], strings.TrimSpace(strings.TrimLeft(line[i:], " \t="))
	}
	return line, ""
}

// isPattern reports whether an alias is a wildcard rather than a real host.
// `Host *` configures defaults; it is not a machine anyone can connect to.
func isPattern(s string) bool {
	return strings.ContainsAny(s, "*?!")
}

func parseAnnotation(line string) (Annotation, bool) {
	body := strings.TrimSpace(strings.TrimLeft(line, "#"))
	const prefix = "rove:"
	if !strings.HasPrefix(strings.ToLower(body), prefix) {
		return Annotation{}, false
	}
	var a Annotation
	for _, field := range strings.Fields(body[len(prefix):]) {
		k, v, has := strings.Cut(field, "=")
		switch strings.ToLower(k) {
		case "ignore":
			a.Ignore = true
		case "env":
			if has {
				a.Env = v
			}
		case "tags":
			if has {
				a.Tags = strings.Split(v, ",")
			}
		}
	}
	return a, true
}

func expandInclude(rest string) []string {
	home, _ := os.UserHomeDir()
	var out []string
	for _, pat := range strings.Fields(rest) {
		pat = strings.Trim(pat, `"`)
		switch {
		case strings.HasPrefix(pat, "~/"):
			pat = filepath.Join(home, pat[2:])
		case !filepath.IsAbs(pat):
			// OpenSSH resolves a relative user Include against ~/.ssh.
			pat = filepath.Join(home, ".ssh", pat)
		}
		matches, err := filepath.Glob(pat)
		if err != nil || len(matches) == 0 {
			continue
		}
		out = append(out, matches...)
	}
	return out
}

// DefaultConfigPath is the user's ssh config.
func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh", "config")
}
