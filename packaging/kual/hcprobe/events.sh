#!/bin/sh
# Log file and LIPC events for 15 min to /mnt/us/hcprobe-events.txt.
# Goal: find events that can trigger a sync (instead of polling).
DIR="$(cd "$(dirname "$0")" && pwd)"
PID=/tmp/hcprobe-events.pid
say() { command -v eips >/dev/null 2>&1 && eips 1 2 "$1                              "; }
if [ -f "$PID" ] && kill -0 "$(cat "$PID")" 2>/dev/null; then
	say "Events watch is already running."
	exit 0
fi
cp "$DIR/bin/probe-go123" /tmp/hcprobe-ev && chmod 755 /tmp/hcprobe-ev
nohup /tmp/hcprobe-ev events >/dev/null 2>&1 &
echo $! > "$PID"
say "Events watch started (15 min)."
