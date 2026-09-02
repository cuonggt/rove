#!/bin/sh
# rove process list, contract version 1.
#
# Read-only. POSIX sh. Run on demand for one host, never as part of a fleet
# refresh: a process table per host per refresh would dwarf everything else
# on the wire to answer a question nobody asked.

echo "rove-processes 1"

limit=60

# GNU procps can sort on the host, which keeps the interesting rows when the
# list is truncated. busybox cannot, so the client sorts what it is given.
# Every branch filters to rows whose first field is a pid. Some ps
# implementations print a header even when asked not to, and a header
# counted as a process makes the total wrong and the list look truncated.
list=$(ps -eo pid=,user=,pcpu=,pmem=,rss=,args= --sort=-pcpu 2>/dev/null | awk '$1 ~ /^[0-9]+$/')
if [ -n "$list" ]; then
    echo "proc.fields=pid,user,cpu,mem,rss,args"
    echo "proc.sorted=cpu"
else
    list=$(ps -eo pid=,user=,pcpu=,pmem=,rss=,args= 2>/dev/null | awk '$1 ~ /^[0-9]+$/')
    if [ -n "$list" ]; then
        echo "proc.fields=pid,user,cpu,mem,rss,args"
    else
        # busybox has neither -e nor percentages, and may reject the "="
        # form that suppresses headers; the client drops any header row.
        list=$(ps -o pid,user,vsz,comm 2>/dev/null | awk '$1 ~ /^[0-9]+$/')
        if [ -n "$list" ]; then
            echo "proc.fields=pid,user,vsz,comm"
        fi
    fi
fi

if [ -z "$list" ]; then
    echo "proc.error=no usable ps on this host"
else
    total=$(echo "$list" | wc -l | tr -d ' ')
    echo "proc.total=$total"
    echo "$list" | head -n "$limit" | sed 's/^[[:space:]]*/proc=/'
fi
