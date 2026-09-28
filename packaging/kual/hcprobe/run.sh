#!/bin/sh
# Hardcover probe: collect device facts and test the Go binaries.
# Writes /mnt/us/hcprobe-report.txt (visible over USB).
# Reads only. Changes nothing on the Kindle.

DIR="$(cd "$(dirname "$0")" && pwd)"
OUT=/mnt/us/hcprobe-report.txt
TMP=/tmp/hcprobe

say() { command -v eips >/dev/null 2>&1 && eips 1 2 "$1                              "; }
sec() { printf '\n== %s ==\n' "$1"; }

say "Hardcover probe: running..."
mkdir -p "$TMP"

{
	echo "hcprobe report v0.1"
	sec "date";      date; date -u
	sec "kernel";    uname -a
	sec "firmware";  cat /etc/prettyversion.txt 2>&1; cat /etc/version.txt 2>&1
	# First 6 chars of the device ID give the model code. The rest (serial) is not recorded.
	sec "model";     cut -c1-6 /proc/usid 2>&1
	sec "cpu";       cat /proc/cpuinfo 2>&1
	sec "memory";    grep -E 'MemTotal|MemFree' /proc/meminfo 2>&1
	sec "tools";     for t in sqlite3 lipc-get-prop eips curl python python3 timeout; do printf '%s: %s\n' "$t" "$(command -v $t || echo missing)"; done
	sec "wifi";      lipc-get-prop com.lab126.wifid cmState 2>&1
	sec "databases"; ls -l /var/local/*.db 2>&1
	sec "cc.db schema (table and column names only, no book data)"
	if command -v sqlite3 >/dev/null 2>&1 && [ -f /var/local/cc.db ]; then
		sqlite3 /var/local/cc.db .schema 2>&1
	else
		echo "skipped (no sqlite3 or no cc.db)"
	fi
	sec "lipc reader props"
	for p in "com.lab126.reader.readingtimer readingProgressType" \
	         "com.lab126.reader.readingtimer getReadingProgressTypes"; do
		echo "$p: $(lipc-get-prop $p 2>&1)"
	done

	for b in probe-go123 probe-go126; do
		sec "binary $b"
		# /mnt/us is FAT (no exec bit). Copy to /tmp and run from there.
		cp "$DIR/bin/$b" "$TMP/$b" && chmod 755 "$TMP/$b"
		"$TMP/$b" 2>&1
		echo "exit code: $?"
	done
	sec "end"
} > "$OUT" 2>&1

rm -rf "$TMP"
sync
say "Hardcover probe: done. Connect USB, send hcprobe-report.txt"
