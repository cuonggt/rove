#!/bin/sh
# rove directory sizes, contract version 1.
#
# Read-only. POSIX sh. Answers "what is filling this up" for one path.
#
# The path arrives as every remaining argument rather than as $1: ssh
# concatenates argv and the remote shell re-splits it, so a mount point
# containing a space would otherwise arrive in pieces. The caller rejects
# shell metacharacters before it gets here.

path="$*"
[ -n "$path" ] || path="/"

echo "rove-du 1"
echo "du.path=$path"

# Walking a large filesystem takes minutes, and this runs inside a request
# with a deadline. A capped run that says it was capped beats a hang.
budget=20

# du writes errors to stderr and sizes to stdout; both are folded together
# here because writing a temporary file would break the read-only promise.
# They are told apart by shape: a size line begins with a digit.
if command -v timeout >/dev/null 2>&1; then
    out=$(timeout "$budget" du -x -k -d 1 -- "$path" 2>&1)
    rc=$?
else
    out=$(du -x -k -d 1 -- "$path" 2>&1)
    rc=$?
fi

# Some du builds have no -d. Fall back to a total for the path itself,
# which is less useful but not wrong.
case "$out" in
    *"unrecognized option"*|*"illegal option"*|*"invalid option"*|*"Unknown option"*)
        out=$(du -x -k -s -- "$path" 2>&1)
        rc=$?
        echo "du.shallow=1"
        ;;
esac

if [ "$rc" = "124" ]; then
    # timeout(1) reports 124. Whatever was collected is still worth showing.
    echo "du.timedout=1"
fi

# Count what could not be read. An unprivileged du skips unreadable
# directories and still prints a total, so a quiet undercount looks exactly
# like a directory that is genuinely small -- which is the wrong conclusion
# to invite when somebody is hunting for a full disk.
unreadable=$(printf '%s\n' "$out" | grep -c 'Permission denied' 2>/dev/null)
[ -z "$unreadable" ] && unreadable=0
[ "$unreadable" -gt 0 ] && echo "du.unreadable=$unreadable"

sizes=$(printf '%s\n' "$out" | grep '^[0-9]')

if [ -z "$sizes" ]; then
    echo "du.error=$(printf '%s\n' "$out" | grep -v '^[0-9]' | head -n1 | cut -c1-160)"
    exit 0
fi

# Largest first, and capped: a directory with thousands of children would
# otherwise return more rows than anyone will read.
printf '%s\n' "$sizes" | sort -rn | head -n 40 | sed 's/^/entry=/'
