package inventory

import (
	"os"
	"path/filepath"
	"testing"
)

// ssh -G prints its own defaults, not only what the user configured. Both of
// these values mean "no hardware key" despite looking like one.
func TestDefaultsDoNotLookLikeAHardwareKey(t *testing.T) {
	// Each OpenSSH version has its own way of saying "nothing configured":
	// 9 prints the unexpanded variable, 10 prints "internal" for its own
	// built-in FIDO support. A real provider is a path to a library.
	for _, notAProvider := range []string{"$SSH_SK_PROVIDER", "internal", "none", ""} {
		if isConfiguredProvider(notAProvider) {
			t.Errorf("%q must not count as a configured provider", notAProvider)
		}
	}
	if isConfiguredProvider("/usr/lib/opensc-pkcs11.so") != true {
		t.Error("a real provider path should count")
	}

	// The default IdentityFile list names _sk files that do not exist.
	if isHardwareKey("~/.ssh/id_ed25519_sk_definitely_absent") {
		t.Error("a nonexistent key must not be treated as hardware")
	}
}

func TestHardwareKeyRequiresTheFileToExist(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "id_ed25519_sk")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !isHardwareKey(p) {
		t.Error("an existing _sk key should be detected")
	}
	if isHardwareKey(filepath.Join(dir, "id_rsa")) {
		t.Error("an ordinary key is not hardware")
	}
}
