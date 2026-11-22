#!/usr/bin/env bash
# Build a release binary for this fork.
#
#   ./build/release.sh            # -> bin/owncast
#
# The build input is the current commit, not the working tree: the sources are checked
# out into a temporary git worktree at HEAD, so uncommitted changes never enter the
# release artifact and the checkout itself is never modified (no bundle is written into
# static/web, so no restore is needed). The commit that was built is printed.
#
# The installed frontend dependencies are reused (they are build inputs, not source).
# The temporary worktree is removed on every exit path, and the binary is moved to
# bin/owncast only after a successful build, so a failed build leaves the previous
# binary in place.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REV="$(git -C "$ROOT" rev-parse HEAD)"
BASE="$(mktemp -d)"
WORK="$BASE/tree"
cleanup() {
	git -C "$ROOT" worktree remove --force "$WORK" 2>/dev/null || true
	rm -rf "$BASE"
}
trap cleanup EXIT

echo "=== release build from commit $REV ==="
echo "    uncommitted changes do not enter the artifact"

git -C "$ROOT" worktree add --detach "$WORK" "$REV" > /dev/null

# Reuse the installed dependencies; they are build inputs, not source.
if [ -d "$ROOT/web/node_modules" ] && [ ! -e "$WORK/web/node_modules" ]; then
	ln -s "$ROOT/web/node_modules" "$WORK/web/node_modules"
fi

echo "=== 1/2 production web bundle ==="
(cd "$WORK" && ./build/web/bundleWeb.sh)

echo "=== 2/2 go build ==="
(cd "$WORK" && go build -o "$BASE/owncast" .)

mkdir -p "$ROOT/bin"
mv "$BASE/owncast" "$ROOT/bin/owncast"
echo "release binary: $ROOT/bin/owncast (commit $REV)"
