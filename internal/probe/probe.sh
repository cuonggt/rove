#!/bin/sh
# rove probe, contract version 1.
#
# Read-only by construction: no redirection to a file, no sudo, no writes.
# POSIX sh only, because this has to run under dash and busybox ash as well
# as bash. Every section is guarded so that one missing tool degrades one
# section rather than killing the run.
#
# Output is line-oriented key=value. Repeated keys are lists. A client
# ignores keys it does not know, so a newer probe never breaks an older rove.

_t0=''
[ -r /proc/uptime ] && _t0=$(cut -d' ' -f1 /proc/uptime 2>/dev/null)

echo "rove-probe 1"

# --- identity ---------------------------------------------------------
_kind=$(uname -s 2>/dev/null | tr '[:upper:]' '[:lower:]')
[ -n "$_kind" ] && echo "sys.kind=$_kind"
_kernel=$(uname -r 2>/dev/null) && [ -n "$_kernel" ] && echo "sys.kernel=$_kernel"
_arch=$(uname -m 2>/dev/null) && [ -n "$_arch" ] && echo "sys.arch=$_arch"

if [ -r /etc/os-release ]; then
    _os=$(sed -n 's/^PRETTY_NAME=//p' /etc/os-release 2>/dev/null | tr -d '"' | head -n1)
    [ -n "$_os" ] && echo "sys.os=$_os"
fi

if [ -r /proc/uptime ]; then
    echo "sys.uptime_s=$(cut -d' ' -f1 /proc/uptime | cut -d. -f1)"
fi

# --- cpu --------------------------------------------------------------
# Raw counters only. Computing a percentage here would mean sleeping on
# every host on every refresh, and fractional sleep is not portable anyway.
# The client keeps the previous sample and takes the delta.
_cores=$(nproc 2>/dev/null)
if [ -z "$_cores" ] && [ -r /proc/cpuinfo ]; then
    _cores=$(grep -c '^processor' /proc/cpuinfo 2>/dev/null)
fi
[ -n "$_cores" ] && echo "cpu.cores=$_cores"

[ -r /proc/stat ] && echo "cpu.stat=$(head -n1 /proc/stat)"
[ -r /proc/loadavg ] && echo "load=$(cut -d' ' -f1,2,3 /proc/loadavg)"

# --- memory -----------------------------------------------------------
if [ -r /proc/meminfo ]; then
    _total=$(awk '/^MemTotal:/{print $2; exit}' /proc/meminfo 2>/dev/null)
    [ -n "$_total" ] && echo "mem.total_kb=$_total"

    _avail=$(awk '/^MemAvailable:/{print $2; exit}' /proc/meminfo 2>/dev/null)
    if [ -z "$_avail" ]; then
        # MemAvailable arrived in 3.14; approximate it on older kernels.
        _avail=$(awk '/^MemFree:|^Buffers:|^Cached:/{s+=$2} END{if (s>0) print s}' /proc/meminfo 2>/dev/null)
    fi
    [ -n "$_avail" ] && echo "mem.available_kb=$_avail"

    _swtotal=$(awk '/^SwapTotal:/{print $2; exit}' /proc/meminfo 2>/dev/null)
    _swfree=$(awk '/^SwapFree:/{print $2; exit}' /proc/meminfo 2>/dev/null)
    [ -n "$_swtotal" ] && echo "swap.total_kb=$_swtotal"
    [ -n "$_swfree" ] && echo "swap.free_kb=$_swfree"
fi

# --- filesystems ------------------------------------------------------
# -P is POSIX output: one line per filesystem, stable column order.
#
# Columns are found by locating the first all-numeric field rather than by
# index, because a device name can contain spaces (macOS prints the
# automounter as "map auto_home", which shifts every later column). The
# mount point is emitted last so that it may contain spaces too.
#
# Pseudo-filesystems are filtered by the client, which can be changed
# without redeploying a script to every host.
df -P -k 2>/dev/null | awk '
NR > 1 {
    n = 0
    for (i = 2; i <= NF; i++) if ($i ~ /^[0-9]+$/) { n = i; break }
    if (n == 0 || n + 4 > NF) next

    dev = $1
    for (j = 2; j < n; j++) dev = dev "_" $j

    mount = $(n + 4)
    for (j = n + 5; j <= NF; j++) mount = mount " " $j

    print "fs=" dev " " $n " " $(n + 1) " " mount
}'

# --- network ----------------------------------------------------------
if command -v ip >/dev/null 2>&1; then
    ip -o -4 addr show 2>/dev/null | awk '{split($4,a,"/"); print "net=" $2 " " a[1]}'
elif command -v ifconfig >/dev/null 2>&1; then
    ifconfig 2>/dev/null | awk '
        /^[a-zA-Z0-9]/ { iface=$1; sub(":$","",iface) }
        /inet (addr:)?[0-9]/ { for (i=1;i<=NF;i++) if ($i ~ /^(addr:)?[0-9]+\./) { a=$i; sub("addr:","",a); print "net=" iface " " a; break } }'
fi

# --- services ---------------------------------------------------------
if command -v systemctl >/dev/null 2>&1; then
    echo "svc.init=systemd"
    _state=$(systemctl is-system-running 2>/dev/null)
    [ -n "$_state" ] && echo "svc.state=$_state"
    # systemctl can be installed and still unable to reach the system bus:
    # no dbus, a container, a restricted account. Swallowing that error
    # would report an empty failed-unit list, which reads as "nothing is
    # wrong" when the truth is "could not tell".
    if _failed=$(systemctl list-units --failed --plain --no-legend --no-pager 2>/dev/null); then
        echo "svc.query=ok"
        [ -n "$_failed" ] && echo "$_failed" | awk 'NF>0 {print "svc.failed=" $1}'
    else
        echo "svc.query=error"
    fi
elif command -v rc-status >/dev/null 2>&1; then
    echo "svc.init=openrc"
elif [ -d /etc/init.d ]; then
    echo "svc.init=sysvinit"
else
    echo "svc.init=unknown"
fi

# --- timing -----------------------------------------------------------
if [ -n "$_t0" ] && [ -r /proc/uptime ]; then
    _t1=$(cut -d' ' -f1 /proc/uptime 2>/dev/null)
    awk -v a="$_t0" -v b="$_t1" 'BEGIN{ d=(b-a)*1000; if (d<0) d=0; printf "probe.ms=%d\n", d }'
fi
