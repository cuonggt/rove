package exec

import (
	"context"
	"strings"

	"github.com/cuonggt/rove/internal/model"
)

// Classify turns a transport failure into something a person can act on.
// A bare "unreachable" is the failure mode this function exists to prevent.
func Classify(alias string, err error, stderr string) (model.Status, string, string) {
	if err == nil {
		return model.StatusOK, "", ""
	}
	if err == context.DeadlineExceeded {
		return model.StatusTimeout, "probe exceeded its deadline", ""
	}
	s := strings.ToLower(stderr)

	switch {
	case strings.Contains(s, "remote host identification has changed"):
		return model.StatusHostKey,
			"host key changed since last connection",
			"verify the host, then: ssh-keygen -R " + alias

	case strings.Contains(s, "host key verification failed"),
		strings.Contains(s, "no matching host key"):
		return model.StatusHostKey,
			"host key not trusted",
			"ssh " + alias + "   (accept the fingerprint once)"

	case strings.Contains(s, "permission denied"),
		strings.Contains(s, "no supported authentication"),
		strings.Contains(s, "too many authentication failures"):
		return model.StatusAuth,
			"authentication failed",
			"ssh " + alias + "   (unlock the key, or add it to ssh-agent)"

	case strings.Contains(s, "could not resolve hostname"),
		strings.Contains(s, "name or service not known"),
		strings.Contains(s, "nodename nor servname"):
		return model.StatusDNS, "hostname does not resolve", ""

	case strings.Contains(s, "connection refused"):
		return model.StatusRefused, "connection refused on the ssh port", ""

	case strings.Contains(s, "connection timed out"),
		strings.Contains(s, "operation timed out"),
		strings.Contains(s, "timed out"):
		return model.StatusTimeout, "connection timed out", ""

	case strings.Contains(s, "no route to host"),
		strings.Contains(s, "network is unreachable"):
		return model.StatusTimeout, "no route to host", ""
	}

	line := firstLine(stderr)
	if line == "" {
		line = err.Error()
	}
	return model.StatusProbeError, line, ""
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		// ssh prefixes its own diagnostics; they are the useful ones but the
		// banner lines around them are not.
		if l == "" || strings.HasPrefix(l, "@") {
			continue
		}
		return l
	}
	return ""
}
