#!/bin/sh
# Run one hcbridge command in the background, so KUAL returns at once.
# /mnt/us is FAT (no exec bit): copy the binary to /tmp and run it there.
DIR="$(cd "$(dirname "$0")" && pwd)"
BIN=/tmp/hcbridge
cp "$DIR/bin/hcbridge" "$BIN" && chmod 755 "$BIN"
CID=""
[ -f "$DIR/client_id.txt" ] && CID="$(tr -d ' \r\n' < "$DIR/client_id.txt")"
nohup "$BIN" "$1" ${CID:+-client-id "$CID"} >/dev/null 2>&1 &
