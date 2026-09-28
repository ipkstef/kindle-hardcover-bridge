#!/bin/sh
# Rating research: what does the end-of-book "Before you go..." dialog
# (stars, Goodreads shelf) change on the device? 20 min.
# Output: /mnt/us/hcprobe-rating/ (same layout as sleep research).
DIR="$(cd "$(dirname "$0")" && pwd)"
PID=/tmp/hcprobe-sleep.pid
say() { command -v eips >/dev/null 2>&1 && eips 1 2 "$1                              "; }
if [ -f "$PID" ] && kill -0 "$(cat "$PID")" 2>/dev/null; then
	say "A research run is already active."
	exit 0
fi
rm -rf /mnt/us/hcprobe-rating
cp "$DIR/bin/probe-go123" /tmp/hcprobe-slp && chmod 755 /tmp/hcprobe-slp
PUBS="com.lab126.InBookDialogService,com.lab126.JournalingService,com.lab126.KSDKLibrary.UserData,com.lab126.KSDKLibrary.Actions,com.lab126.KPPMainApp,com.lab126.odot,com.lab126.fastMetrics,com.lab126.reader.KPPBridge,com.lab126.dc.KPPBridge,com.lab126.kppContextMenu,com.lab126.sharing,com.lab126.kwis,com.lab126.pillow,com.lab126.booklet,com.lab126.todo,com.lab126.readnow,com.lab126.whisperstore,com.lab126.AnnotationManager"
nohup /tmp/hcprobe-slp sleep -out /mnt/us/hcprobe-rating -secs 1200 -tick 60 -pubs "$PUBS" -scan /mnt/us/system >/dev/null 2>&1 &
echo $! > "$PID"
say "Rating research started (20 min)."
