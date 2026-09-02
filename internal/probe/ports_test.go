package probe

import (
	"os"
	"testing"
)

func TestParsePorts(t *testing.T) {
	in := "rove-ports 1\n" +
		"port.limited=1\n" +
		"port.source=ss\n" +
		"listen=tcp 0.0.0.0 22 77 sshd\n" +
		"listen=tcp 127.0.0.1 5432 88 postgres\n" +
		"listen=tcp :: 22 - -\n" +
		"conn=ESTAB 14\n" +
		"conn=LISTEN 2\n"

	p, err := ParsePorts([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Available() || !p.Limited {
		t.Errorf("source=%q limited=%v", p.Source, p.Limited)
	}
	if len(p.Listeners) != 3 {
		t.Fatalf("got %d listeners", len(p.Listeners))
	}
	if p.Established() != 14 {
		t.Errorf("established = %d", p.Established())
	}
	if p.ExposedCount() != 2 {
		t.Errorf("exposed = %d, want 2 (0.0.0.0 and ::)", p.ExposedCount())
	}
}

// A blank owner means "could not read", not a process named "-".
func TestMissingOwnerIsNotAProcessName(t *testing.T) {
	p, err := ParsePorts([]byte("rove-ports 1\nport.source=ss\nlisten=tcp :: 22 - -\n"))
	if err != nil {
		t.Fatal(err)
	}
	l := p.Listeners[0]
	if l.HasProcess || l.Process != "" {
		t.Errorf("process = %q has=%v; a dash means unknown", l.Process, l.HasProcess)
	}
}

// The distinction the raw tools do not make: reachable from the network, or
// only from the host itself.
func TestExposureOrdering(t *testing.T) {
	in := "rove-ports 1\nport.source=ss\n" +
		"listen=tcp 127.0.0.1 5432 1 postgres\n" +
		"listen=tcp 0.0.0.0 443 2 nginx\n" +
		"listen=tcp 127.0.0.1 6379 3 redis\n" +
		"listen=tcp :: 22 4 sshd\n"
	p, _ := ParsePorts([]byte(in))

	got := p.Ordered()
	if !got[0].Exposed() || !got[1].Exposed() {
		t.Fatal("network-facing sockets must sort first")
	}
	if got[0].Port != 22 || got[1].Port != 443 {
		t.Errorf("exposed sockets should then sort by port, got %d then %d", got[0].Port, got[1].Port)
	}
	if !got[2].Loopback() || !got[3].Loopback() {
		t.Error("loopback sockets belong after the exposed ones")
	}
}

func TestGoldenPorts(t *testing.T) {
	// Four real capture paths: iproute2, busybox netstat, and the /proc
	// reader that covers minimal images shipping neither tool.
	for _, name := range []string{
		"ss-root.txt", "ss-unprivileged.txt",
		"netstat-busybox.txt", "proc.txt",
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/ports/" + name)
			if err != nil {
				t.Skip("fixture missing")
			}
			p, err := ParsePorts(raw)
			if err != nil {
				t.Fatal(err)
			}
			if !p.Available() {
				t.Fatalf("source = %q", p.Source)
			}
			if len(p.Listeners) == 0 {
				t.Fatal("a host running sshd is listening on something")
			}
			var sawSSH bool
			for _, l := range p.Listeners {
				if l.Port == 22 {
					sawSSH = true
				}
			}
			if !sawSSH {
				t.Error("port 22 missing from a host we reached over ssh")
			}
			// Root reads owners; an unprivileged account does not. Each
			// must report which of those it was.
			if p.Limited {
				for _, l := range p.Listeners {
					if l.HasProcess {
						t.Errorf("limited capture should not name owners: %+v", l)
					}
				}
			}
		})
	}
}
