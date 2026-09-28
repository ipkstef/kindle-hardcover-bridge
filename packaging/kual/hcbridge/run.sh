#!/bin/sh
# Run one hcbridge command in the background, so KUAL returns at once.
# /mnt/us is FAT (no exec bit): copy the binary to /tmp and run it there.
DIR="$(cd "$(dirname "$0")" && pwd)"
BIN=/tmp/hcbridge
# Copy then rename: the daemon may be running from $BIN ("text file busy").
cp "$DIR/bin/hcbridge" "$BIN.new" && chmod 755 "$BIN.new" && mv -f "$BIN.new" "$BIN"
CID=""
[ -f "$DIR/client_id.txt" ] && CID="$(tr -d ' \r\n' < "$DIR/client_id.txt")"
nohup "$BIN" "$1" ${CID:+-client-id "$CID"} >/dev/null 2>&1 &
