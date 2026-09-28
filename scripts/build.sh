#!/bin/sh
# Build the KUAL package: dist/hcbridge.zip
# Built with Go 1.23 (lowest kernel floor, 2.6.32). See docs/open-questions.md.
# Optional: HC_CLIENT_ID=... bakes the public OAuth client ID into the binary.
set -eu
cd "$(dirname "$0")/.."
PKG=packaging/kual/hcbridge
mkdir -p "$PKG/bin" dist
GOTOOLCHAIN=go1.23.12 CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 \
	go build -trimpath -ldflags "-s -w -X main.clientID=${HC_CLIENT_ID:-}" \
	-o "$PKG/bin/hcbridge" ./cmd/hcbridge
rm -rf dist/hcbridge-pkg dist/hcbridge.zip
mkdir -p dist/hcbridge-pkg/extensions
cp -r "$PKG" dist/hcbridge-pkg/extensions/
(cd dist/hcbridge-pkg && zip -qr ../hcbridge.zip extensions)
rm -rf dist/hcbridge-pkg
echo "built dist/hcbridge.zip"
