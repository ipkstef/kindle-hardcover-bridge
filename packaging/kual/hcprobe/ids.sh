#!/bin/sh
# List ISBN / ASIN inside each book file to /mnt/us/hcprobe-ids.txt. No titles.
DIR="$(cd "$(dirname "$0")" && pwd)"
say() { command -v eips >/dev/null 2>&1 && eips 1 2 "$1                              "; }
say "Scanning books..."
cp "$DIR/bin/probe-go123" /tmp/hcprobe-ids && chmod 755 /tmp/hcprobe-ids
/tmp/hcprobe-ids ids
rm -f /tmp/hcprobe-ids
sync
say "Done. Connect USB, send hcprobe-ids.txt"
