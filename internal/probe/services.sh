#!/bin/sh
# rove service list, contract version 1.
#
# Read-only: this reports what the init system says, and starts, stops and
# restarts nothing. POSIX sh, run on demand for one host.

echo "rove-services 1"

if command -v systemctl >/dev/null 2>&1; then
    echo "svc.init=systemd"
    state=$(systemctl is-system-running 2>/dev/null)
    [ -n "$state" ] && echo "svc.state=$state"

    if ! units=$(systemctl list-units --type=service --all --plain --no-legend --no-pager 2>&1); then
        # Report the reason rather than an empty list. "systemd with no
        # services" and "systemd we could not query" look identical
        # otherwise, and only one of them is fine.
        echo "svc.error=$(echo "$units" | head -n1)"
        exit 0
    fi
    echo "svc.query=ok"

    echo "$units" \
      | awk 'NF >= 4 {
            # A unit systemd cannot resolve is prefixed with a bullet, which
            # shifts every column; drop it before reading them.
            first = 1
            if ($1 == "*" || $1 == "\xe2\x97\x8f") first = 2
            name = $first; load = $(first+1); active = $(first+2); sb = $(first+3)
            desc = ""
            for (i = first+4; i <= NF; i++) desc = desc (i > first+4 ? " " : "") $i
            print "unit=" name " " load " " active " " sb " " desc
        }'

elif command -v rc-status >/dev/null 2>&1; then
    echo "svc.init=openrc"
    rc-status -a 2>/dev/null | awk '
        /\[/ {
            name = $1
            state = $0
            sub(/.*\[[ ]*/, "", state)
            sub(/[ ]*\].*/, "", state)
            if (name != "" && state != "") print "unit=" name " loaded " state " " state " "
        }'

else
    echo "svc.init=unsupported"
fi
