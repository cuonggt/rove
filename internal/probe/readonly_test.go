package probe

import (
	"regexp"
	"strings"
	"testing"
)

// The README promises the probe never writes to a remote host. That promise
// is only as good as what enforces it.
// scripts is every shell script shipped to a remote host. All of them carry
// the same promise, so all of them are linted.
func scripts() map[string]string {
	return map[string]string{
		"probe.sh":     Script,
		"processes.sh": ProcessScript,
		"services.sh":  ServiceScript,
		"logs.sh":      LogScript,
	}
}

func TestProbeScriptIsReadOnly(t *testing.T) {
	banned := []*regexp.Regexp{
		regexp.MustCompile(`\bsudo\b`),
		regexp.MustCompile(`\bdoas\b`),
		regexp.MustCompile(`\brm\b`),
		regexp.MustCompile(`\bmv\b`),
		regexp.MustCompile(`\bcp\b`),
		regexp.MustCompile(`\btouch\b`),
		regexp.MustCompile(`\btee\b`),
		regexp.MustCompile(`\bmkdir\b`),
		regexp.MustCompile(`\bchmod\b`),
		regexp.MustCompile(`\bchown\b`),
		regexp.MustCompile(`\bdd\b`),
		regexp.MustCompile(`\bln\b`),
		regexp.MustCompile(`\bcurl\b`),
		regexp.MustCompile(`\bwget\b`),
	}

	for name, src := range scripts() {
		assertReadOnly(t, name, src, banned)
	}
}

func assertReadOnly(t *testing.T, name, src string, banned []*regexp.Regexp) {
	t.Helper()
	shell := shellOnly(src)
	raw := strings.Split(src, "\n")

	for i, line := range strings.Split(shell, "\n") {
		code := line
		if strings.TrimSpace(code) == "" {
			continue
		}
		for _, re := range banned {
			if re.MatchString(code) {
				t.Errorf("%s line %d writes or escalates: %s", name, i+1, strings.TrimSpace(raw[i]))
			}
		}
		for _, redir := range findRedirects(code) {
			if redir != "/dev/null" {
				t.Errorf("%s line %d redirects to %q; only /dev/null is allowed", name, i+1, redir)
			}
		}
	}
}

// shellOnly blanks out comments and single-quoted regions, keeping line
// structure intact. Both checks below inspect shell syntax, and this script
// embeds awk programs in single quotes: `NR > 1` is a comparison inside awk,
// not a redirection, and `==` there is not a bashism. Without this the
// linters flag the awk and miss the shell.
func shellOnly(src string) string {
	out := []byte(src)
	inQuote, inComment := false, false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\n':
			inComment = false
			continue
		case inComment:
			out[i] = ' '
		case inQuote:
			if c == '\'' {
				inQuote = false
			}
			out[i] = ' '
		case c == '\'':
			inQuote = true
			out[i] = ' '
		case c == '#' && (i == 0 || src[i-1] == '\n' || src[i-1] == ' ' || src[i-1] == '\t'):
			inComment = true
			out[i] = ' '
		}
	}
	return string(out)
}

// findRedirects returns the target of every output redirection on a line,
// ignoring the fd-duplication form (2>&1) which writes nothing new.
func findRedirects(code string) []string {
	var out []string
	for i := 0; i < len(code); i++ {
		if code[i] != '>' {
			continue
		}
		rest := code[i+1:]
		rest = strings.TrimPrefix(rest, ">") // append
		if strings.HasPrefix(rest, "&") {
			continue // 2>&1 duplicates an fd
		}
		target := strings.TrimSpace(rest)
		if end := strings.IndexAny(target, " \t;|&)"); end >= 0 {
			target = target[:end]
		}
		if target != "" {
			out = append(out, target)
		}
	}
	return out
}

// The script must stay POSIX: it runs under dash and busybox ash, not only
// bash. These are the constructs that silently work on a developer's Mac and
// fail on an Alpine host.
func TestProbeScriptIsPOSIX(t *testing.T) {
	bashisms := map[string]*regexp.Regexp{
		"[[ ]] test":     regexp.MustCompile(`\[\[`),
		"local keyword":  regexp.MustCompile(`\blocal\b`),
		"array syntax":   regexp.MustCompile(`=\(`),
		"function kwd":   regexp.MustCompile(`\bfunction\s+\w+`),
		"$'...' quoting": regexp.MustCompile(`\$'`),
		"== comparison":  regexp.MustCompile(`\s==\s`),
	}
	for file, src := range scripts() {
		shell := shellOnly(src)
		for name, re := range bashisms {
			if re.MatchString(shell) {
				t.Errorf("%s uses %s, which is not POSIX sh", file, name)
			}
		}
		if !strings.HasPrefix(src, "#!/bin/sh") {
			t.Errorf("%s must declare #!/bin/sh", file)
		}
	}
}
