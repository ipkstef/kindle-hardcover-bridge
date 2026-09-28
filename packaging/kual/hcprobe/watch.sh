#!/bin/sh
# Watch reading state for 10 minutes. Log every 10 s to /mnt/us/hcprobe-watch.txt.
# Goal: find when the stock reader writes progress to cc.db and sidecar files.
# Reads only. Changes nothing on the Kindle.

# Env overrides are for local tests only.
OUT=${HCPROBE_OUT:-/mnt/us/hcprobe-watch.txt}
PID=/tmp/hcprobe-watch.pid
DB=${HCPROBE_DB:-/var/local/cc.db}
SECS=${HCPROBE_SECS:-600}
STEP=${HCPROBE_STEP:-10}

say() { command -v eips >/dev/null 2>&1 && eips 1 2 "$1                              "; }

# Run the loop in the background so KUAL returns at once.
if [ "$1" != "--loop" ]; then
	if [ -f "$PID" ] && kill -0 "$(cat "$PID")" 2>/dev/null; then
		say "Watch is already running."
		exit 0
	fi
	nohup /bin/sh "$0" --loop >/dev/null 2>&1 &
	say "Watch started (10 min). Open a book and read."
	exit 0
fi

echo $$ > "$PID"
trap 'rm -f "$PID"' EXIT

q() { sqlite3 "$DB" "$1" 2>&1; }

{
	echo "hcprobe watch v0.1  start $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
	echo "cols: utc | power | key | percent | readState | lastAccess | db files (mtime size name) | sidecar newest (mtime size ext)"
} > "$OUT"

end=$(( $(date +%s) + SECS ))
while [ "$(date +%s)" -lt "$end" ]; do
	now=$(date -u '+%H:%M:%S')
	power=$(lipc-get-prop com.lab126.powerd state 2>/dev/null)
	row=$(q "select substr(p_cdeKey,1,8), p_percentFinished, p_readState, p_lastAccess, p_location
	         from Entries where p_type='Entry:Item' order by p_lastAccess desc limit 1;")
	path=${row##*|}
	info=${row%|*}
	dbf=$(stat -c '%Y %s %n' "$DB"* 2>/dev/null | tr '\n' ' ')
	sdr=""
	if [ -n "$path" ] && [ -d "${path%.*}.sdr" ]; then
		# Log only the file extension, not the name (it holds the book title).
		newest=$(ls -t "${path%.*}.sdr" 2>/dev/null | head -1)
		sdr="$(stat -c '%Y %s' "${path%.*}.sdr/$newest" 2>/dev/null) *.${newest##*.}"
	fi
	echo "$now | $power | $info | $dbf| $sdr" >> "$OUT"
	sleep "$STEP"
done
echo "end $(date -u '+%Y-%m-%dT%H:%M:%SZ')" >> "$OUT"
sync
