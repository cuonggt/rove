#!/bin/sh
# rove log tail, contract version 1.
#
# Read-only. POSIX sh. Reads the journal or the syslog files and writes
# nothing. It never follows: a follow would never return, and this runs
# inside a request with a deadline.
#
# Arguments: $1 unit name (empty for the whole system), $2 line count.
# The caller validates the unit name; nothing here interpolates it into a
# shell command.

# ssh concatenates argv and the remote shell re-splits it, so an empty
# first argument would vanish and shift the line count into its place.
# "-" is the explicit way to say "the whole system".
unit=$1
[ "$unit" = "-" ] && unit=""

lines=$2
case "$lines" in
    ''|*[!0-9]*) lines=200 ;;
esac

echo "rove-logs 1"
[ -n "$unit" ] && echo "log.unit=$unit"

# systemd's journal ACL shows an unprivileged account only the messages
# from its own uid. Such an account can therefore read its own ssh session
# lines while seeing nothing at all from a system unit -- which looks
# exactly like a unit that never logged. Group membership is the only
# reliable way to tell those apart.
journal_privileged() {
    [ "$(id -u 2>/dev/null)" = "0" ] && return 0
    for g in $(id -nG 2>/dev/null); do
        case "$g" in
            systemd-journal|adm) return 0 ;;
        esac
    done
    return 1
}

emit() {
    # A log line can contain anything, including our own key=value syntax.
    # Prefixing every line means the parser never has to guess.
    sed 's/^/log.line=/'
}

if command -v journalctl >/dev/null 2>&1; then
    # -q suppresses the hint block journalctl prints for unprivileged
    # accounts, which otherwise arrives as if it were log output.
    if [ -n "$unit" ]; then
        out=$(journalctl -q -u "$unit" -n "$lines" --no-pager --output=short-iso 2>&1)
    else
        out=$(journalctl -q -n "$lines" --no-pager --output=short-iso 2>&1)
    fi
    rc=$?

    if [ "$rc" -ne 0 ]; then
        echo "log.source=none"
        echo "log.error=$(echo "$out" | head -n1)"
        exit 0
    fi

    # journalctl marks an empty result with "-- No entries --" rather than
    # printing nothing, so strip its own placeholders before deciding.
    body=$(printf '%s\n' "$out" | grep -v '^-- ' | sed '/^[[:space:]]*$/d')

    if journal_privileged; then
        privileged=yes
    else
        privileged=no
        # Even a non-empty tail is only this account's own messages, so say
        # so rather than presenting a partial log as the whole story.
        echo "log.limited=1"
    fi

    if [ -n "$body" ]; then
        echo "log.source=journald"
        printf '%s\n' "$body" | emit
        exit 0
    fi

    # Exiting 0 did not mean it worked.
    if [ "$privileged" = "yes" ]; then
        echo "log.source=journald"
        echo "log.error=no entries for this unit"
    else
        echo "log.source=none"
        echo "log.error=this account sees only its own messages; add it to the systemd-journal or adm group"
    fi
    exit 0
fi

# No journal. Fall back to whichever syslog file this distribution uses and
# is actually readable; these are normally root:adm 0640.
for f in /var/log/syslog /var/log/messages; do
    if [ -r "$f" ]; then
        echo "log.source=$f"
        if [ -n "$unit" ]; then
            # Best effort: syslog has no unit concept, so match the tag.
            grep -F "$unit" "$f" 2>/dev/null | tail -n "$lines" | emit
        else
            tail -n "$lines" "$f" 2>/dev/null | emit
        fi
        exit 0
    fi
done

echo "log.source=none"
if [ -e /var/log/syslog ] || [ -e /var/log/messages ]; then
    echo "log.error=a syslog file exists but is not readable by this account"
else
    echo "log.error=no journald and no readable syslog file on this host"
fi
