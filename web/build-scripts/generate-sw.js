/* eslint-disable no-console, @typescript-eslint/no-require-imports */
// Generates the production service worker over the static export in out/,
// run by `npm run build` after `next build`. This replaces next-pwa, which
// was unmaintained and webpack-only (the build now uses Turbopack).
//
// The worker precaches only viewer-facing assets for fast repeat loads and
// PWA installability. Because Turbopack emits opaque content-hashed filenames
// under _next/static/chunks/ (not pages/admin/**), viewer chunks are derived
// explicitly from the exported non-admin HTML files rather than globbing all
// of _next/static/**. Admin chunks, platform logos, and admin styles are never
// precached.

const fs = require('fs');
const path = require('path');
const { generateSW } = require('workbox-build');

const outDir = path.join(__dirname, '..', 'out');
const MAX_PRECACHE_BYTES = 8 * 1024 * 1024; // Guard against accidental admin/locale bloat.

function walkFiles(dir, predicate) {
  const results = [];
  if (!fs.existsSync(dir)) return results;

  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      const rel = path.relative(outDir, fullPath).split(path.sep).join('/');
      if (rel === 'admin' || rel.startsWith('admin/') || rel === '_next') {
        continue;
      }
      results.push(...walkFiles(fullPath, predicate));
    } else if (predicate(fullPath)) {
      results.push(fullPath);
    }
  }
  return results;
}

function collectViewerNextAssets(rootDir) {
  const htmlFiles = walkFiles(rootDir, file => file.endsWith('.html'));
  const assetSet = new Set();
  const nextAssetRe = /\/_next\/static\/[^"'\s<>)]+/g;

  const recordAsset = rawPath => {
    const clean = rawPath.split('?')[0].split('#')[0].replace(/^\/+/, '');
    if (!clean || clean.endsWith('.map')) return;
    // Every Service Worker capable browser supports WOFF2; skip legacy .woff fonts.
    if (clean.endsWith('.woff')) return;
    const absPath = path.join(rootDir, clean);
    if (fs.existsSync(absPath) && fs.statSync(absPath).isFile()) {
      assetSet.add(clean);
    }
  };

  for (const htmlFile of htmlFiles) {
    const html = fs.readFileSync(htmlFile, 'utf8');
    const matches = html.match(nextAssetRe) || [];
    for (const match of matches) {
      recordAsset(match);
    }
  }

  // Also include assets referenced by viewer CSS chunks (e.g. woff2 fonts).
  for (const relPath of Array.from(assetSet)) {
    if (!relPath.endsWith('.css')) continue;
    const css = fs.readFileSync(path.join(rootDir, relPath), 'utf8');
    const matches = css.match(nextAssetRe) || [];
    for (const match of matches) {
      recordAsset(match);
    }
  }

  return Array.from(assetSet).sort();
}

async function run() {
  if (!fs.existsSync(path.join(outDir, 'index.html'))) {
    throw new Error(`[generate-sw] no static export found at ${outDir}, run next build first`);
  }

  const viewerNextAssets = collectViewerNextAssets(outDir);
  if (viewerNextAssets.length === 0) {
    throw new Error('[generate-sw] failed to discover any viewer _next/static assets');
  }

  const { count, size, warnings } = await generateSW({
    globDirectory: outDir,
    globPatterns: [
      ...viewerNextAssets,
      'fonts/**/*',
      'img/**/*',
      'styles/**/*',
      'manifest.json',
      'serviceWorker.js',
      '*.png',
    ],
    globIgnores: [
      '**/*.map',
      'fonts/**/*.woff',
      'fonts/inter/Inter-*.woff2',
      'img/platformlogos/**',
      'styles/admin/**',
    ],
    swDest: path.join(outDir, 'sw.js'),
    skipWaiting: true,
    clientsClaim: true,
    // Drop precaches from previous builds. Without it the browser keeps serving
    // the old chunks from a stale cache after an upgrade.
    cleanupOutdatedCaches: true,
    // Include Web Push handlers in the single root-scoped service worker so
    // /sw.js and /serviceWorker.js never fight over scope "/".
    importScripts: ['/serviceWorker.js'],
    // Manifest URLs are generated relative to out/, make them absolute.
    modifyURLPrefix: { '': '/' },
    // Only content-hashed chunk files skip revision busting; unhashed manifests
    // (such as _buildManifest.js / _ssgManifest.js) must still be revisioned.
    dontCacheBustURLsMatching: /^\/_next\/static\/chunks\//,
  });

  warnings.forEach(w => console.warn('[generate-sw]', w));

  // Fail the build loudly rather than ship a broken, leaky, or bloated manifest.
  const sw = fs.readFileSync(path.join(outDir, 'sw.js'), 'utf8');
  const manifest = sw.match(/precacheAndRoute\(\[.*?\]/s)?.[0] ?? '';
  if (count < 10) {
    throw new Error(`[generate-sw] suspiciously small precache manifest (${count} entries)`);
  }
  if (/admin/i.test(manifest)) {
    throw new Error('[generate-sw] admin assets leaked into the precache manifest');
  }
  if (size > MAX_PRECACHE_BYTES) {
    throw new Error(
      `[generate-sw] precache manifest too large (${(size / 1024 / 1024).toFixed(1)} MB > ${(MAX_PRECACHE_BYTES / 1024 / 1024).toFixed(1)} MB)`,
    );
  }
  console.log(`[generate-sw] precached ${count} files, ${(size / 1024 / 1024).toFixed(1)} MB`);
}

run().catch(err => {
  console.error(err);
  process.exit(1);
});
