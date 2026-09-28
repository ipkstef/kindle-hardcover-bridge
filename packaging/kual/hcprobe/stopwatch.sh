#!/bin/sh
# Stop a running watch (item 3) and events watch (item 5) early.
say() { command -v eips >/dev/null 2>&1 && eips 1 2 "$1                              "; }
stopped=""
for P in /tmp/hcprobe-watch.pid /tmp/hcprobe-events.pid; do
	if [ -f "$P" ] && kill "$(cat "$P")" 2>/dev/null; then
		stopped="yes"
	fi
	rm -f "$P"
done
[ -f /mnt/us/hcprobe-watch.txt ] && echo "stopped $(date -u '+%Y-%m-%dT%H:%M:%SZ')" >> /mnt/us/hcprobe-watch.txt
sync
if [ -n "$stopped" ]; then say "Watch stopped."; else say "No watch is running."; fi
