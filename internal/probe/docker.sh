#!/bin/sh
# rove container listing, contract version 1.
#
# Read-only. POSIX sh. Detects a container runtime and lists what it is
# running. It starts, stops and removes nothing.
#
# Fields are tab separated rather than space separated: an image reference,
# a status phrase and a port mapping all contain spaces or colons, and
# guessing where one ends is how a parser starts lying.

echo "rove-docker 1"

TAB=$(printf '\t')
FMT="{{.ID}}${TAB}{{.State}}${TAB}{{.Names}}${TAB}{{.Image}}${TAB}{{.Status}}${TAB}{{.Ports}}"

probe_runtime() {
    runtime=$1

    command -v "$runtime" >/dev/null 2>&1 || return 1
    echo "docker.cli=$runtime"

    # Unlike the journal, the daemon says plainly why it refused, so asking
    # is more reliable than inferring from group membership.
    out=$("$runtime" ps -a --no-trunc --format "$FMT" 2>&1)
    rc=$?

    if [ "$rc" -ne 0 ]; then
        echo "docker.source=none"
        # Match on the concepts, not on one release's phrasing: Docker 29
        # dropped "Cannot connect to the Docker daemon", which earlier
        # versions printed and which a narrower matcher would still expect.
        case "$out" in
            *"permission denied"*|*"Permission denied"*)
                echo "docker.error=this account cannot reach the $runtime socket; add it to the $runtime group"
                ;;
            *"daemon is not running"*|*"daemon is running"*|*"onnect"*|*"refused"*|\
            *"dial unix"*|*"no such file or directory"*)
                echo "docker.error=$runtime is installed but its daemon is not reachable"
                ;;
            *)
                # Never pass a whole paragraph through to a table cell.
                echo "docker.error=$(echo "$out" | head -n1 | cut -c1-160)"
                ;;
        esac
        return 0
    fi

    echo "docker.source=$runtime"

    ver=$("$runtime" version --format '{{.Server.Version}}' 2>/dev/null)
    [ -n "$ver" ] && echo "docker.version=$ver"

    # An empty list is a real answer: the runtime is there with nothing on
    # it, which is different from not being able to look.
    if [ -n "$out" ]; then
        echo "$out" | sed 's/^/container=/'
    fi
    return 0
}

for rt in docker podman nerdctl; do
    if probe_runtime "$rt"; then
        exit 0
    fi
done

echo "docker.source=none"
echo "docker.error=no container runtime found on this host"
