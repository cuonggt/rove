package inventory

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func aliases(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Alias
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestScanCollectsLiteralAliasesOnly(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "config", `
Host *
    ServerAliveInterval 30

Host prod-api prod-worker
    HostName 10.0.0.1

Host !staging-*
    User nobody

Host db-01
    HostName 10.0.0.9

Match host anything
    User matched
`)
	got, err := Scan(p)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, aliases(got), []string{"prod-api", "prod-worker", "db-01"})
}

func TestScanDeduplicates(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "config", "Host web\nHost web\nHost api\n")
	got, _ := Scan(p)
	eq(t, aliases(got), []string{"web", "api"})
}

func TestScanFollowsInclude(t *testing.T) {
	dir := t.TempDir()
	inc := write(t, dir, "extra.conf", "Host from-include\n")
	p := write(t, dir, "config", "Include "+inc+"\nHost from-main\n")

	got, err := Scan(p)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, aliases(got), []string{"from-include", "from-main"})
}

func TestScanSurvivesIncludeCycle(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.conf")
	b := filepath.Join(dir, "b.conf")
	write(t, dir, "a.conf", "Host from-a\nInclude "+b+"\n")
	write(t, dir, "b.conf", "Host from-b\nInclude "+a+"\n")

	done := make(chan []Entry, 1)
	go func() { g, _ := Scan(a); done <- g }()
	select {
	case got := <-done:
		eq(t, aliases(got), []string{"from-a", "from-b"})
	case <-timeout():
		t.Fatal("Scan did not terminate on an Include cycle")
	}
}

func TestAnnotationsAttachToNextHost(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "config", `
# rove: env=prod tags=web,edge
Host prod-web

# rove: ignore
Host bastion

Host plain
`)
	got, _ := Scan(p)
	eq(t, aliases(got), []string{"prod-web", "bastion", "plain"})

	if got[0].Ann.Env != "prod" {
		t.Errorf("env = %q, want prod", got[0].Ann.Env)
	}
	if len(got[0].Ann.Tags) != 2 || got[0].Ann.Tags[0] != "web" {
		t.Errorf("tags = %v", got[0].Ann.Tags)
	}
	if !got[1].Ann.Ignore {
		t.Error("bastion should be ignored")
	}
	// An annotation must not leak past the host it precedes.
	if got[2].Ann.Ignore || got[2].Ann.Env != "" {
		t.Errorf("annotation leaked onto %q: %+v", got[2].Alias, got[2].Ann)
	}
}

func TestScanAcceptsEqualsForm(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "config", "Host=web-01\n")
	got, _ := Scan(p)
	eq(t, aliases(got), []string{"web-01"})
}

func TestMissingConfigIsAnEmptyFleet(t *testing.T) {
	got, err := Scan(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("missing config should not error, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestInferEnv(t *testing.T) {
	cases := map[string]string{
		"prod-api":       "prod",
		"production.db":  "prod",
		"staging-worker": "staging",
		"stg_cache":      "staging",
		"qa-1":           "qa",
		"home-server":    "",
		"produce-box":    "", // must not match on a prefix substring
	}
	for in, want := range cases {
		if got := inferEnv(in); got != want {
			t.Errorf("inferEnv(%q) = %q, want %q", in, got, want)
		}
	}
}
