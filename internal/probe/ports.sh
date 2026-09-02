#!/bin/sh
# rove listening sockets, contract version 1.
#
# Read-only. POSIX sh. Lists what is listening and summarises established
# connections by state. It never dumps every connection: a busy host has
# tens of thousands, and the count is the useful part.

echo "rove-ports 1"

# Only root sees the process behind another user's socket. An unprivileged
# account gets the ports but blank owners, which must not be presented as
# "no process" -- the same trap as an unreadable journal.
#
# This is decided per source rather than once up front: the /proc reader
# cannot name an owner for anybody, root included.
if [ "$(id -u 2>/dev/null)" = "0" ]; then
    is_root=yes
else
    is_root=no
fi

owners_from_privilege() {
    if [ "$is_root" = "yes" ]; then
        echo "port.privileged=1"
    else
        echo "port.limited=1"
    fi
}

# ss prints the local endpoint as addr:port, where addr may be an IPv6
# literal in brackets. Splitting on the *last* colon is the only rule that
# works for both families.
parse_ss() {
    awk '
        $1 == "Netid" || $1 == "State" { next }
        NF >= 5 {
            proto = $1
            endpoint = $5
            n = split(endpoint, _, "")
            pos = 0
            for (i = length(endpoint); i > 0; i--) {
                if (substr(endpoint, i, 1) == ":") { pos = i; break }
            }
            if (pos == 0) next
            addr = substr(endpoint, 1, pos - 1)
            port = substr(endpoint, pos + 1)
            gsub(/^\[|\]$/, "", addr)
            if (addr == "*") addr = "0.0.0.0"

            pid = "-"
            name = "-"
            if (match($0, /users:\(\("[^"]+",pid=[0-9]+/)) {
                spec = substr($0, RSTART, RLENGTH)
                if (match(spec, /"[^"]+"/)) {
                    name = substr(spec, RSTART + 1, RLENGTH - 2)
                }
                if (match(spec, /pid=[0-9]+/)) {
                    pid = substr(spec, RSTART + 4, RLENGTH - 4)
                }
            }
            print "listen=" proto " " addr " " port " " pid " " name
        }'
}

# netstat columns differ: proto, recv, send, local, foreign, state, pid/name.
parse_netstat() {
    awk '
        $1 ~ /^(tcp|udp)/ && NF >= 4 {
            proto = $1
            endpoint = $4
            pos = 0
            for (i = length(endpoint); i > 0; i--) {
                if (substr(endpoint, i, 1) == ":") { pos = i; break }
            }
            if (pos == 0) next
            addr = substr(endpoint, 1, pos - 1)
            port = substr(endpoint, pos + 1)

            pid = "-"
            name = "-"
            if (match($0, /[0-9]+\/[^ ]+/)) {
                spec = substr($0, RSTART, RLENGTH)
                split(spec, parts, "/")
                pid = parts[1]
                name = parts[2]
            }
            print "listen=" proto " " addr " " port " " pid " " name
        }'
}

if command -v ss >/dev/null 2>&1; then
    echo "port.source=ss"
    owners_from_privilege
    ss -tulnp 2>/dev/null | parse_ss

    # State counts, not the connections themselves.
    ss -tan 2>/dev/null | awk 'NR > 1 && NF >= 1 { c[$1]++ } END { for (s in c) print "conn=" s " " c[s] }'

elif command -v netstat >/dev/null 2>&1; then
    echo "port.source=netstat"
    owners_from_privilege
    netstat -tulnp 2>/dev/null | parse_netstat
    netstat -tan 2>/dev/null | awk '$1 ~ /^tcp/ && NF >= 6 { c[$6]++ } END { for (s in c) print "conn=" s " " c[s] }'

elif [ -r /proc/net/tcp ]; then
    # Minimal images ship neither iproute2 nor net-tools, and telling
    # somebody to install a package first defeats the point of the tool.
    # /proc is always there. Owners are not: mapping a socket to a process
    # means walking every /proc/*/fd, which is far too expensive for this.
    echo "port.source=proc"
    # Always limited here, even for root: resolving an owner would mean
    # walking every /proc/*/fd to match socket inodes, which costs far more
    # than this screen is worth.
    echo "port.limited=1"

    for f in /proc/net/tcp /proc/net/tcp6 /proc/net/udp /proc/net/udp6; do
        [ -r "$f" ] || continue
        proto=$(basename "$f" | sed 's/6$//')
        awk -v proto="$proto" -v want_state="$( [ "${f#*udp}" != "$f" ] && echo 07 || echo 0A )" '
            function hex2dec(h,   i, c, v, n) {
                h = toupper(h); n = 0
                for (i = 1; i <= length(h); i++) {
                    c = substr(h, i, 1)
                    v = index("0123456789ABCDEF", c) - 1
                    if (v < 0) return -1
                    n = n * 16 + v
                }
                return n
            }
            # A 32-bit word in /proc is written little-endian, so the octets
            # read back to front.
            function ipv4(h) {
                return hex2dec(substr(h,7,2)) "." hex2dec(substr(h,5,2)) "." \
                       hex2dec(substr(h,3,2)) "." hex2dec(substr(h,1,2))
            }
            # IPv6 is four such words; each one reverses independently.
            function ipv6(h,   w, i, bytes, out, quad, zero) {
                bytes = ""
                for (w = 0; w < 4; w++) {
                    for (i = 4; i >= 1; i--) {
                        bytes = bytes substr(h, w * 8 + (i - 1) * 2 + 1, 2)
                    }
                }
                zero = 1
                for (i = 1; i <= 32; i++) if (substr(bytes, i, 1) != "0") zero = 0
                if (zero) return "::"
                out = ""
                for (i = 0; i < 8; i++) {
                    quad = substr(bytes, i * 4 + 1, 4)
                    sub(/^0+/, "", quad)
                    if (quad == "") quad = "0"
                    out = out (i ? ":" : "") tolower(quad)
                }
                return out
            }
            NR > 1 && NF >= 4 {
                if ($4 != want_state) next
                split($2, ep, ":")
                addr = (length(ep[1]) > 8) ? ipv6(ep[1]) : ipv4(ep[1])
                port = hex2dec(ep[2])
                if (port <= 0) next
                print "listen=" proto " " addr " " port " - -"
            }' "$f"
    done

    # State 01 is ESTABLISHED; the rest are counted the same way.
    awk 'NR > 1 && NF >= 4 { c[$4]++ }
         END {
             names["01"] = "ESTAB"; names["06"] = "TIME-WAIT"
             names["08"] = "CLOSE-WAIT"; names["0A"] = "LISTEN"
             for (s in c) if (s in names) print "conn=" names[s] " " c[s]
         }' /proc/net/tcp /proc/net/tcp6 2>/dev/null

else
    echo "port.source=none"
    echo "port.error=no ss, no netstat, and /proc/net/tcp is unreadable"
fi
