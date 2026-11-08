#!/usr/bin/env bash
# SRT integration suite entry point (upstream convention: test/automated/<area>/run.sh).
# Delegates to the canonical QA harness so there is one implementation of the matrix.
#
#   ./run.sh                  # build current HEAD, then run the SRT e2e matrix
#   ./run.sh --no-build       # reuse artifacts/bin/owncast-qa
#   ./run.sh --keep-server    # leave the managed instance running for debugging
#   QA_NETNS=1 ./run.sh       # isolated unprivileged network namespace
#
# Exit codes: 0 pass (blocked reported), 1 fail, 3 blocked at this HEAD.
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# This runner lives inside the Owncast repo; the workspace root is one level up.
REPO_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null || true)"
if [[ -z "$REPO_ROOT" ]]; then REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"; fi
WORKSPACE_ROOT="$(cd "$REPO_ROOT/.." && pwd)"
exec "$WORKSPACE_ROOT/tools/scripts/run-srt-e2e.sh" "$@"
