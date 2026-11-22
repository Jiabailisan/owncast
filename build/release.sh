#!/usr/bin/env bash
# Build a release binary for this fork.
#
#   ./build/release.sh            # -> bin/owncast
#
# The build input is the current commit, not the working tree: the sources are checked
# out into a temporary git worktree at HEAD, so uncommitted changes never enter the
# release artifact and the checkout itself is never modified.
#
# The frontend dependencies are installed inside that worktree from the lockfile of the
# commit being built, with the npm version the project pins, and the bundle then runs with
# --offline so it cannot install again and change the resolution. The caller's
# node_modules, lockfile and working tree are never touched.
#
# The worktree is removed on every exit path, and the binary is moved to bin/owncast only
# after a successful build, so a failed build leaves the previous binary in place.
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

# The project pins the npm version in web/package.json; use it when the local npm differs.
PINNED_NPM="$(node -p "require('$WORK/web/package.json').engines.npm" 2>/dev/null || true)"
run_npm() {
	if [ -n "$PINNED_NPM" ] && [ "$(npm --version 2>/dev/null)" != "$PINNED_NPM" ]; then
		npx --yes "npm@$PINNED_NPM" "$@"
	else
		npm "$@"
	fi
}

echo "=== 1/3 frontend dependencies from the committed lockfile ==="
echo "    node $(node --version), npm $(npm --version) (pinned: ${PINNED_NPM:-unset})"
(cd "$WORK/web" && run_npm ci --prefer-offline)

echo "=== 2/3 production web bundle ==="
(cd "$WORK" && ./build/web/bundleWeb.sh --offline)

echo "=== 3/3 go build ==="
(cd "$WORK" && go build -o "$BASE/owncast" .)

mkdir -p "$ROOT/bin"
mv "$BASE/owncast" "$ROOT/bin/owncast"
echo "release binary: $ROOT/bin/owncast (commit $REV)"
