#!/bin/sh
# Stop a running watch early.
PID=/tmp/hcprobe-watch.pid
say() { command -v eips >/dev/null 2>&1 && eips 1 2 "$1                              "; }
if [ -f "$PID" ] && kill "$(cat "$PID")" 2>/dev/null; then
	rm -f "$PID"
	echo "stopped $(date -u '+%Y-%m-%dT%H:%M:%SZ')" >> /mnt/us/hcprobe-watch.txt
	sync
	say "Watch stopped."
else
	say "Watch is not running."
fi
