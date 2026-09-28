#!/bin/sh
# Sleep research: record all position sources at each event for 45 min.
# Output: /mnt/us/hcprobe-sleep/ (timeline.txt, lipc-all.txt, NN-<event>/).
DIR="$(cd "$(dirname "$0")" && pwd)"
PID=/tmp/hcprobe-sleep.pid
say() { command -v eips >/dev/null 2>&1 && eips 1 2 "$1                              "; }
if [ -f "$PID" ] && kill -0 "$(cat "$PID")" 2>/dev/null; then
	say "Sleep research is already running."
	exit 0
fi
rm -rf /mnt/us/hcprobe-sleep
cp "$DIR/bin/probe-go123" /tmp/hcprobe-slp && chmod 755 /tmp/hcprobe-slp
nohup /tmp/hcprobe-slp sleep >/dev/null 2>&1 &
echo $! > "$PID"
say "Sleep research started (45 min)."
