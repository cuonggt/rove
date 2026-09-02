#!/bin/sh
# rove process detail, contract version 1.
#
# Read-only. POSIX sh. Everything comes from /proc, so this works on a host
# with no tooling installed at all.
#
# /proc/PID/environ is deliberately never read. It routinely holds database
# passwords and API keys, and putting those on a screen -- or into whatever
# scrollback, recording or bug report the screen ends up in -- is not a
# trade worth making for a diagnostic.

pid=$1
case "$pid" in
    ''|*[!0-9]*) pid="" ;;
esac

echo "rove-proc 1"

if [ -z "$pid" ]; then
    echo "proc.error=no process id given"
    exit 0
fi
echo "proc.pid=$pid"

if [ ! -d "/proc/$pid" ]; then
    echo "proc.error=no process $pid; it may have exited since the list was taken"
    exit 0
fi

limited=0

# --- identity ---------------------------------------------------------
if [ -r "/proc/$pid/cmdline" ]; then
    # argv is NUL separated; a command with spaces in one argument is
    # indistinguishable from several arguments once flattened, but the
    # whole line is what a person recognises.
    cmd=$(tr '\0' ' ' < "/proc/$pid/cmdline" | sed 's/[[:space:]]*$//')
    [ -n "$cmd" ] && echo "proc.cmdline=$cmd"
fi

# --- status -----------------------------------------------------------
if [ -r "/proc/$pid/status" ]; then
    awk '
        /^Name:/    { print "proc.comm=" $2 }
        /^State:/   { print "proc.state=" $2; $1=""; $2=""; sub(/^ +/,"");
                      gsub(/[()]/,""); if ($0 != "") print "proc.state_text=" $0 }
        /^PPid:/    { print "proc.ppid=" $2 }
        /^Threads:/ { print "proc.threads=" $2 }
        /^Uid:/     { print "proc.uid=" $2 }
        /^VmRSS:/   { print "proc.rss_kb=" $2 }
        /^VmSize:/  { print "proc.vsz_kb=" $2 }
    ' "/proc/$pid/status" 2>/dev/null
else
    limited=1
fi

# The owning account, resolved without assuming getent exists.
uid=$(awk '/^Uid:/ {print $2; exit}' "/proc/$pid/status" 2>/dev/null)
if [ -n "$uid" ] && [ -r /etc/passwd ]; then
    user=$(awk -F: -v u="$uid" '$3 == u {print $1; exit}' /etc/passwd)
    [ -n "$user" ] && echo "proc.user=$user"
fi

# The parent's name, so lineage reads as something rather than a number.
ppid=$(awk '/^PPid:/ {print $2; exit}' "/proc/$pid/status" 2>/dev/null)
if [ -n "$ppid" ] && [ -r "/proc/$ppid/comm" ]; then
    echo "proc.parent=$(cat "/proc/$ppid/comm" 2>/dev/null)"
fi

# --- elapsed ----------------------------------------------------------
# Field 22 of /proc/PID/stat is the start time in clock ticks since boot.
# The comm field is parenthesised and may contain spaces, so counting from
# the closing bracket is the only safe way to index the rest.
if [ -r "/proc/$pid/stat" ] && [ -r /proc/uptime ]; then
    ticks=$(getconf CLK_TCK 2>/dev/null)
    case "$ticks" in ''|*[!0-9]*) ticks=100 ;; esac
    up=$(cut -d' ' -f1 /proc/uptime | cut -d. -f1)
    start=$(sed 's/.*) //' "/proc/$pid/stat" 2>/dev/null | awk '{print $20}')
    case "$start" in
        ''|*[!0-9]*) ;;
        *) echo "proc.elapsed_s=$((up - start / ticks))" ;;
    esac
fi

# --- paths ------------------------------------------------------------
# These are symlinks readable only by the owner or root; failing to read
# them is normal and must not look like the process having no executable.
for what in exe cwd; do
    if target=$(readlink "/proc/$pid/$what" 2>/dev/null) && [ -n "$target" ]; then
        echo "proc.$what=$target"
    else
        limited=1
    fi
done

if [ -r "/proc/$pid/fd" ]; then
    # Counted by globbing rather than by parsing ls, which keeps this
    # dependent on nothing but the shell itself.
    fds=0
    for _fd in "/proc/$pid/fd"/*; do
        [ -e "$_fd" ] && fds=$((fds + 1))
    done
    echo "proc.fds=$fds"
else
    limited=1
fi

# --- container --------------------------------------------------------
# On a container host most processes belong to containers, and ps gives no
# hint of it. The cgroup path does.
if [ -r "/proc/$pid/cgroup" ]; then
    cid=$(sed -n 's/.*[-\/]\([0-9a-f]\{64\}\).*/\1/p' "/proc/$pid/cgroup" 2>/dev/null | head -n1)
    if [ -n "$cid" ]; then
        echo "proc.container=$cid"
    else
        # Podman and some runtimes use a named scope rather than a raw id.
        scope=$(sed -n 's/.*\/\(libpod-[^ ]*\)\.scope.*/\1/p' "/proc/$pid/cgroup" 2>/dev/null | head -n1)
        [ -n "$scope" ] && echo "proc.container=$scope"
    fi
fi

[ "$limited" = "1" ] && echo "proc.limited=1"
exit 0
