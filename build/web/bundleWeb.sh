#!/usr/bin/env bash
# shellcheck disable=SC2059

set -o errexit
set -o nounset
set -o pipefail

OFFLINE=
while [[ $# -gt 0 ]]; do
	case $1 in
	--offline)
		OFFLINE=1
		;;
	esac
	shift
done

# Change to the root directory of the repository
cd "$(git rev-parse --show-toplevel)"

cd web

if [ ! "$OFFLINE" ]; then
	echo "Installing npm modules for the owncast web..."
	# Use npm ci so web/node_modules strictly matches package-lock.json and
	# never drifts onto a newer Next.js patch with incompatible inline scripts.
	# Keep stderr: discarding it turns real failures (wrong node version,
	# missing npm, registry errors) into a silent exit.
	npm --silent ci
fi

echo "Building owncast web..."
rm -rf .next out
# No output filtering: piping through grep hid real build errors, and under
# pipefail a successful build with no matching lines would fail the script.
# Runs the package.json build script so the service worker generation step
# (build-scripts/generate-sw.js) stays chained after next build.
npm run --silent build

# Guard against a build that "succeeds" without producing the static export.
# Known failure mode: unsupported node versions (e.g. v23) make next build
# exit 0 after seconds having compiled nothing. Check before touching the
# existing bundle so a bad build never deletes a working static/web.
if [ ! -d ./out ]; then
	echo "ERROR: next build produced no ./out directory despite exiting successfully." >&2
	echo "This usually means the node version in use ($(node -v)) is unsupported by this Next.js version." >&2
	exit 1
fi

# Verify no exported HTML page emits an un-nonced executable inline <script>
# tag that would be blocked by Owncast's strict script-src CSP header.
node -e '
const fs = require("fs");
const path = require("path");
const violations = [];
function walk(dir) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      walk(full);
    } else if (entry.isFile() && entry.name.endsWith(".html")) {
      const html = fs.readFileSync(full, "utf8");
      const re = /<script\b([^>]*)>([\s\S]*?)<\/script>/gi;
      let match;
      while ((match = re.exec(html)) !== null) {
        const attrs = match[1] || "";
        const body = (match[2] || "").trim();
        if (!body) continue;
        if (/\bsrc\s*=/i.test(attrs) || /\bnonce\s*=/i.test(attrs)) continue;
        const typeMatch = attrs.match(/\btype\s*=\s*["\x27]?([^"\x27\s>]+)/i);
        const type = typeMatch ? typeMatch[1].toLowerCase() : "";
        if (type === "application/json" || type === "application/ld+json" || type === "text/template") {
          continue;
        }
        violations.push(`${path.relative("out", full)}: ${body.slice(0, 80)}`);
      }
    }
  }
}
walk("out");
if (violations.length > 0) {
  console.error("ERROR: un-nonced inline <script> tags detected in static export (violates Owncast CSP):");
  for (const v of violations) console.error("  - " + v);
  process.exit(1);
}
'

echo "Copying web project to dist directory..."

# Remove the old one
rm -rf ../static/web

# Copy over the new one
mv ./out ../static/web

echo "Done."
