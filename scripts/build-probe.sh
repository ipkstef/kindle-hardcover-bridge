#!/bin/sh
# Build the KUAL probe package: dist/hcprobe.zip
# Two binaries: Go 1.23 (kernel 2.6.32+) and Go 1.26 (kernel 3.2+).
set -eu
cd "$(dirname "$0")/.."
PKG=packaging/kual/hcprobe
rm -rf dist && mkdir -p dist/extensions "$PKG/bin"
for v in 1.23.12:go123 1.26.8:go126; do
	GOTOOLCHAIN="go${v%%:*}" CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 \
		go build -trimpath -ldflags "-s -w" -o "$PKG/bin/probe-${v##*:}" ./cmd/probe
done
cp -r "$PKG" dist/extensions/
(cd dist && zip -qr hcprobe.zip extensions)
echo "built dist/hcprobe.zip"
