#!/usr/bin/env bash
# Build a release binary for this fork.
#
# The frontend bundle is built from this checkout first, so the binary never embeds a
# stale static/web: the tracked bundle is generated output and is restored afterwards.
#
#   ./build/release.sh          # -> bin/owncast
set -euo pipefail

cd "$(dirname "$0")/.."

echo "=== 1/2 production web bundle from this checkout ==="
./build/web/bundleWeb.sh

echo "=== 2/2 go build ==="
go build -o bin/owncast .
echo "release binary: bin/owncast"

# The bundle is generated output; keep the working tree clean.
git checkout -- static/web 2>/dev/null || true
