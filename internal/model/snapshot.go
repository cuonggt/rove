package model

import (
	"strings"
	"time"
)

// Snapshot is one probe of one host. Fields are zero when the host did not
// report them, which is normal: a section that could not run degrades to
// absent rather than failing the probe.
type Snapshot struct {
	At time.Time

	Kind    string // linux, darwin, ...
	OS      string // PRETTY_NAME
	Kernel  string
	Arch    string
	UptimeS int64

	Cores   int
	CPU     CPUStat
	HasCPU  bool
	Load    [3]float64
	HasLoad bool

	MemTotalKB, MemAvailKB  int64
	SwapTotalKB, SwapFreeKB int64

	Filesystems []Filesystem
	Interfaces  []Iface

	Init        string // systemd, openrc, sysvinit, unknown
	InitState   string // systemctl is-system-running
	SvcQuery    string // ok, error: whether the init system could be read
	FailedUnits []string

	// ContainerRuntime names a runtime binary found on the host, if any.
	// It says nothing about whether the daemon is reachable.
	ContainerRuntime string

	ProbeMS int64
}

// MemUsedPercent reports memory pressure, or false when the host did not
// report memory.
func (s Snapshot) MemUsedPercent() (float64, bool) {
	if s.MemTotalKB <= 0 {
		return 0, false
	}
	used := s.MemTotalKB - s.MemAvailKB
	if used < 0 {
		used = 0
	}
	return float64(used) / float64(s.MemTotalKB) * 100, true
}

// CPUStat holds the raw counters from the first line of /proc/stat. The probe
// never computes a percentage: doing so would mean sleeping on every host on
// every refresh. The client takes a delta between two snapshots instead.
type CPUStat struct {
	User, Nice, System, Idle, IOWait, IRQ, SoftIRQ, Steal uint64
}

func (c CPUStat) Total() uint64 {
	return c.User + c.Nice + c.System + c.Idle + c.IOWait + c.IRQ + c.SoftIRQ + c.Steal
}

func (c CPUStat) Idleish() uint64 { return c.Idle + c.IOWait }

// CPUPercent is busy time between two samples. It reports false when the
// samples cannot yield a meaningful figure, which is the normal case on the
// very first refresh: there is nothing to compare against yet.
func CPUPercent(prev, cur CPUStat) (float64, bool) {
	pt, ct := prev.Total(), cur.Total()
	if pt == 0 || ct <= pt {
		return 0, false
	}
	total := ct - pt
	idle := cur.Idleish()
	if idle < prev.Idleish() {
		// Counters went backwards: the host rebooted between samples.
		return 0, false
	}
	idle -= prev.Idleish()
	if idle > total {
		return 0, false
	}
	return float64(total-idle) / float64(total) * 100, true
}

// Filesystem is one line of `df -P -k`.
type Filesystem struct {
	Device  string
	Mount   string
	TotalKB int64
	UsedKB  int64
}

func (f Filesystem) UsedPercent() float64 {
	if f.TotalKB <= 0 {
		return 0
	}
	return float64(f.UsedKB) / float64(f.TotalKB) * 100
}

var pseudoDevices = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "shm": true, "devfs": true,
	"none": true, "udev": true, "proc": true, "sysfs": true,
	"cgroup": true, "cgroup2": true, "squashfs": true, "efivarfs": true,
	"overlay": true,
}

var pseudoMounts = []string{"/proc", "/sys", "/dev", "/run", "/snap", "/var/lib/docker"}

// IsReal reports whether a filesystem is worth showing a person. The filter
// lives here rather than in the probe so that it can be corrected without
// redeploying a script to every host.
func (f Filesystem) IsReal() bool {
	if f.TotalKB <= 0 {
		return false
	}
	// The root filesystem always counts, even when it is an overlay, which
	// is how every container reports its own disk.
	if f.Mount == "/" {
		return true
	}
	if pseudoDevices[f.Device] || strings.HasPrefix(f.Device, "map_") {
		return false
	}
	for _, p := range pseudoMounts {
		if f.Mount == p || strings.HasPrefix(f.Mount, p+"/") {
			return false
		}
	}
	// A file bind-mounted over a config path is not a disk anyone manages.
	// Containers mount /etc/hosts, /etc/hostname and /etc/resolv.conf this
	// way, and each one otherwise reports the whole host disk again.
	if strings.HasPrefix(f.Mount, "/etc/") {
		return false
	}
	return true
}

// RealFilesystems returns only the filesystems worth showing.
func (s Snapshot) RealFilesystems() []Filesystem {
	out := make([]Filesystem, 0, len(s.Filesystems))
	for _, f := range s.Filesystems {
		if f.IsReal() {
			out = append(out, f)
		}
	}
	return out
}

// FullestFilesystem is what the fleet table shows in its disk column: the
// mount closest to running out, not an average across mounts that would hide
// a full /var behind an empty /data.
func (s Snapshot) FullestFilesystem() (Filesystem, bool) {
	var worst Filesystem
	found := false
	for _, f := range s.RealFilesystems() {
		if !found || f.UsedPercent() > worst.UsedPercent() {
			worst, found = f, true
		}
	}
	return worst, found
}

// ServicesReadable reports whether the failed-unit list means anything. An
// init system that is present but unqueryable returns no failures, which is
// indistinguishable from a healthy host unless this is checked.
func (s Snapshot) ServicesReadable() bool {
	if s.Init == "" || s.Init == "unknown" || s.Init == "unsupported" {
		return false
	}
	return s.SvcQuery != "error"
}

// Iface is one network interface with an IPv4 address.
type Iface struct {
	Name string
	Addr string
}

// IsReal excludes loopback. Nobody reaches a server on 127.0.0.1, and
// listing it pushes the address that matters further down the card.
func (i Iface) IsReal() bool {
	return i.Name != "lo" && i.Name != "lo0" && !strings.HasPrefix(i.Addr, "127.")
}

// RealInterfaces returns the addresses worth showing.
func (s Snapshot) RealInterfaces() []Iface {
	out := make([]Iface, 0, len(s.Interfaces))
	for _, i := range s.Interfaces {
		if i.IsReal() {
			out = append(out, i)
		}
	}
	return out
}
