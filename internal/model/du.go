package model

import "sort"

// DirEntry is one directory and the space beneath it.
type DirEntry struct {
	Path string
	KB   int64
}

// DirUsage answers "what is filling this up" for one path.
type DirUsage struct {
	Path    string
	Entries []DirEntry

	// Unreadable counts the directories du was refused. An unprivileged du
	// skips them and still prints a total, so an undercount looks exactly
	// like a directory that is genuinely small. That is precisely the wrong
	// conclusion to invite while somebody hunts a full disk.
	Unreadable int
	// TimedOut means the walk was capped; the figures below are a floor.
	TimedOut bool
	// Shallow means du could not descend, so only the total is known.
	Shallow bool
	Err     string
}

// Total is the size of the requested path itself, which du reports as one
// of the rows rather than separately.
func (d DirUsage) Total() int64 {
	for _, e := range d.Entries {
		if e.Path == d.Path {
			return e.KB
		}
	}
	return 0
}

// Children are the entries below the requested path, largest first.
func (d DirUsage) Children() []DirEntry {
	out := make([]DirEntry, 0, len(d.Entries))
	for _, e := range d.Entries {
		if e.Path != d.Path {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].KB > out[j].KB })
	return out
}

// Exact reports whether the figures can be taken at face value. When false
// every number shown is a floor, and the interface must say so.
func (d DirUsage) Exact() bool {
	return d.Unreadable == 0 && !d.TimedOut && !d.Shallow
}

// Share is what fraction of the parent an entry accounts for, which is the
// number people actually scan for.
func (d DirUsage) Share(e DirEntry) float64 {
	total := d.Total()
	if total <= 0 {
		return 0
	}
	return float64(e.KB) / float64(total) * 100
}
