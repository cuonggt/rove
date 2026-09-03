#!/bin/sh
# rove action, contract version 1.
#
# THIS SCRIPT CHANGES THINGS. It is the only script rove sends that does,
# which is why it lives outside internal/probe: everything there is
# read-only and a test enforces it.
#
# The verb is chosen from a closed set in Go and never built from user
# input. The target is validated there too, and arrives as every remaining
# argument so that ssh's argv concatenation cannot split it.

verb=$1
shift 2>/dev/null || true
target="$*"

echo "rove-act 1"
echo "act.verb=$verb"
[ -n "$target" ] && echo "act.target=$target"

if [ -z "$verb" ] || [ -z "$target" ]; then
    echo "act.error=missing verb or target"
    exit 0
fi

# Privilege is reported, not assumed. sudo -n never prompts: a password
# prompt would hang a session with nobody watching it, exactly like the
# BatchMode problem on the read side.
if [ "$(id -u 2>/dev/null)" = "0" ]; then
    priv=root
elif command -v sudo >/dev/null 2>&1; then
    priv=sudo
else
    priv=none
fi
echo "act.privilege=$priv"

run() {
    if [ "$priv" = "sudo" ]; then
        sudo -n "$@"
    else
        "$@"
    fi
}

out=""
rc=0

case "$verb" in
    service-start)   out=$(run systemctl start   "$target" 2>&1); rc=$? ;;
    service-stop)    out=$(run systemctl stop    "$target" 2>&1); rc=$? ;;
    service-restart) out=$(run systemctl restart "$target" 2>&1); rc=$? ;;

    process-term)    out=$(run kill -TERM "$target" 2>&1); rc=$? ;;
    process-kill)    out=$(run kill -KILL "$target" 2>&1); rc=$? ;;

    container-start)   out=$(run docker start   "$target" 2>&1); rc=$? ;;
    container-stop)    out=$(run docker stop    "$target" 2>&1); rc=$? ;;
    container-restart) out=$(run docker restart "$target" 2>&1); rc=$? ;;

    *)
        echo "act.error=unknown verb"
        exit 0
        ;;
esac

echo "act.exit=$rc"

if [ "$rc" -ne 0 ]; then
    case "$out" in
        *"a password is required"*|*"no tty present"*|*"sudo:"*)
            echo "act.error=this account needs a password for sudo, which rove will not prompt for"
            ;;
        *"Access denied"*|*"not authorized"*|*"Permission denied"*|*"permission denied"*)
            echo "act.error=this account is not permitted to do that"
            ;;
        *)
            echo "act.error=$(printf '%s' "$out" | head -n1 | cut -c1-200)"
            ;;
    esac
    exit 0
fi

# Close the loop: an action that reports success without saying what the
# thing now looks like leaves the reader to go and check anyway.
case "$verb" in
    service-*)
        state=$(run systemctl is-active "$target" 2>/dev/null | head -n1)
        [ -n "$state" ] && echo "act.state=$state"
        ;;
    process-*)
        if [ -d "/proc/$target" ]; then
            echo "act.state=still running"
        else
            echo "act.state=gone"
        fi
        ;;
    container-*)
        state=$(run docker inspect -f '{{.State.Status}}' "$target" 2>/dev/null | head -n1)
        [ -n "$state" ] && echo "act.state=$state"
        ;;
esac

echo "act.ok=1"
