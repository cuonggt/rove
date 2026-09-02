package model

// LogTail is a bounded window onto one host's log, either the journal or a
// syslog file.
type LogTail struct {
	// Unit is empty when the tail covers the whole system.
	Unit string
	// Source is journald, a file path, or none. It is shown to the reader,
	// because "the last 200 lines of the journal" and "the last 200 lines
	// of /var/log/messages" are different claims.
	Source string
	Lines  []string
	// Limited means the account could only see its own messages. The tail
	// may look complete and still be missing everything from system units,
	// which is worse than an empty one because it invites a wrong
	// conclusion.
	Limited bool
	// Err explains an empty tail. The common case is not a broken host but
	// an unprivileged account: the journal is readable by root and the
	// systemd-journal group, and syslog files are usually root:adm.
	Err string
}

// Available reports whether any log could be read at all.
func (l LogTail) Available() bool {
	return l.Source != "" && l.Source != "none"
}

// Partial reports a tail the reader should not treat as the whole story.
func (l LogTail) Partial() bool { return l.Limited && l.Available() }

// FromJournal distinguishes a real journal from a scraped syslog file, where
// per-unit filtering is only a substring match.
func (l LogTail) FromJournal() bool {
	return l.Source == "journald"
}
