# Custom player module - contract and example

When `customPlayerPath` is set in Configuration -> General -> Appearance (and no custom
frontend directory is active), the viewer page loads the configured file as an ES module from
`/custom-player/player.js` and mounts it in place of the default player. Everything else on the
page (chat, header, tabs, theme) is untouched.

## Contract (stable)

```ts
export interface OwncastPlayerContext {
  hlsUrl: string;            // e.g. "/hls/stream.m3u8"
  whepUrl?: string;          // present when WebRTC delivery is enabled
  variants: Array<{ index: number; name: string; width?: number; height?: number }>;
  isOnline: boolean;
  autoplay: 'none' | 'muted' | 'unmuted-when-allowed';
  lowLatency: boolean;
  // Host-provided transport helpers (additive):
  //   libs.videojs: the video.js constructor, including its VHS/HLS engine.
  //   libs.hls:     an Hls.js constructor when a future host provides one
  //                 (not bundled today; undefined).
  libs?: { videojs?: unknown; hls?: { isSupported(): boolean } };
  reportPlayback?: (event: unknown) => void;
}

export interface OwncastPlayer {
  destroy(): void;
  setVariant?(index: number): void;
}

// Default export is required.
export default function mount(el: HTMLElement, ctx: OwncastPlayerContext): OwncastPlayer;
```

Rules:
- The module must export a default `mount(el, ctx)` function and return an object with
  `destroy()`.
- The file is served read-only with `Content-Type: text/javascript` and `Cache-Control:
  no-cache`. It is loaded with a same-origin dynamic `import()`, so it must not rely on
  `eval`/inline scripts (the CSP forbids them).
- On load failure or a thrown mount error, the page logs the failure, reports it to the admin
  Appearance page, and falls back to the default player. It never blocks the page.
- WebRTC playback (WHEP), HLS playback and protocol selection are the module's responsibility in
  this mode; use the standard public endpoints (`ctx.hlsUrl`, `ctx.whepUrl`).
- The module is served outside the app bundle, so it cannot use bare imports like `import Hls from
  'hls.js'`. Use the host-provided `ctx.libs.videojs` constructor for HLS playback (video.js
  includes the VHS/HLS engine, so it works in Chromium as well as Safari), and plain
  `RTCPeerConnection` for WHEP. `ctx.libs.hls` is reserved for hosts that provide Hls.js and is
  `undefined` here. Example:
  ```js
  const player = ctx.libs.videojs(videoEl, {
    controls: false, autoplay: ctx.autoplay !== 'none', muted: ctx.autoplay === 'muted',
    sources: [{ src: ctx.hlsUrl, type: 'application/x-mpegURL' }],
  });
  // HLS stats: player.tech(true).vhs?.playlists, player.videoWidth(), player.videoHeight()
  ```
- `reportPlayback(event)`, when present, forwards CMCD-like playback events to the existing
  metrics collector (`POST /api/metrics/cmcd`).

## Minimal example

```js
// custom-player.js
export default function mount(el, ctx) {
  const video = document.createElement('video');
  video.controls = true;
  video.autoplay = ctx.autoplay !== 'none';
  video.muted = ctx.autoplay === 'muted';
  video.playsInline = true;
  video.style.width = '100%';
  el.replaceChildren(video);

  let destroyed = false;
  const source = ctx.whepUrl || ctx.hlsUrl;
  if (ctx.whepUrl) {
    // Minimal WHEP attach: POST an offer and set the answer. Real players should
    // also handle retries, stats and HLS fallback.
    (async () => {
      const pc = new RTCPeerConnection();
      pc.addTransceiver('video', { direction: 'recvonly' });
      pc.addTransceiver('audio', { direction: 'recvonly' });
      pc.ontrack = event => { video.srcObject = event.streams[0]; };
      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      const response = await fetch(ctx.whepUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/sdp' },
        body: pc.localDescription.sdp,
      });
      if (!response.ok) throw new Error('WHEP failed: ' + response.status);
      await pc.setRemoteDescription({ type: 'answer', sdp: await response.text() });
      window.__customPc = pc;
    })().catch(() => {
      video.src = ctx.hlsUrl; // fallback
    });
  } else {
    video.src = ctx.hlsUrl;
  }

  return {
    destroy() {
      if (destroyed) return;
      destroyed = true;
      if (window.__customPc) { try { window.__customPc.close(); } catch (e) {} }
      video.removeAttribute('src');
      video.load();
      video.remove();
    },
  };
}
```

See `docs/examples/custom-player/` for the file itself.


## YouTube Live-style example: `youtube-live-player.js`

`youtube-live-player.js` in this directory is a complete, single-file custom player that
reimplements the YouTube Live player experience on top of the public Owncast endpoints. Point
Configuration -> General -> Appearance -> Custom player path at its absolute path (the server
must be able to read the file; for a packaged deployment copy it next to the server instead of
referencing the workspace):

```bash
# Local development (server and workspace on the same machine)
/absolute/path/to/owncast-rebuild/docs/examples/custom-player/youtube-live-player.js
# Production: copy the file to the server, then set that absolute path, e.g.
#   /srv/owncast/custom-player/youtube-live-player.js
```

Features:

- 16:9 dark player surface, click-to-play, center overlay, buffering spinner, autohiding controls,
  double-click fullscreen, keyboard shortcuts: space/k play-pause, m mute, f fullscreen,
  ArrowLeft/Right or j/l seek, ArrowUp/Down volume, Escape closes the settings menu.
- **Video scaling**: the rendered video always fills the player box (identical box geometry to the
  YouTube reference) while `object-fit: contain` keeps a non-16:9 source uncropped. The HLS
  transport forces video.js' inline `.vjs-tech` sizing to `inset:0; width/height:100% !important`
  (the previous field bug showed the content only in fullscreen); asserted in the E2E at 360px,
  720px and 1200px player widths in both WebRTC and HLS modes, plus in fullscreen.
- 56px bottom overlay following the YouTube reference: play/pause, volume icon + hover slider, a
  **single integrated red-dot "Live" pill** (static/disabled at the live edge; behind live it turns
  into the red **go-to-live** control with `aria-label="Go to live"` and seeks back on click - there
  is no separate "Go to live" button), then settings gear, picture-in-picture and fullscreen.
  No quality/status label in the control bar (quality lives in the settings menu only).
- **Top-left LIVE watermark** on the video (small red "LIVE" pill, non-interactive, hidden when the
  transport is not live or an error is shown).
- Live window progress bar for HLS (seekable DVR window) and a full red bar for WebRTC.
- Settings menu in the YouTube style: `#212121` panel, icon + label + right-aligned value +
  chevron rows, and submenus with a back-arrow header for **Playback speed** (HLS only, 0.5x-2x,
  disabled with an explanatory row on WebRTC), **Quality** ("Auto (720p)" style labels + WebRTC
  variants from `ctx.variants` / HLS levels) and **Playback source** (**Auto / WebRTC / HLS**;
  Auto prefers WebRTC when `ctx.whepUrl` and `RTCPeerConnection` are available; persisted, and
  switching tears down the old transport first). "Stats for nerds" is toggled from this menu.
- Stats for nerds panel replicating YouTube: top-left of the video, 496px wide, 75% black,
  11px/14.3px text, label-left/value-right rows, horizon bars for Connection Speed / Network
  Activity / Buffer Health / Live Latency, and a [x] close control. Rows: Video ID / sCPN
  (WHEP session id or "owncast-hls"), Viewport / Frames, Current / Optimal Res, Volume /
  Normalized, Codecs, Connection Speed, Network Activity, Buffer Health, Live Latency, Live Mode,
  Server / Ingest (protocol, viewers; admin-only ingest bitrate shown as N/A), Mystery Text and
  Date. Missing values are honest N/A (e.g. WebRTC buffer/latency).
- Preferences (source, volume, mute, quality, speed, stats) are persisted in localStorage under
  the `owncast-youtube-player:*` keys. `destroy()` closes the WebRTC peer connection, DELETEs the
  WHEP session, disposes the HLS player and clears all listeners/timers.

Host-provided libraries: Chromium can only play HLS through a library. The viewer passes
`ctx.libs.videojs` (video.js with VHS), which this example uses for the HLS transport. If a host
provides `ctx.libs.hls` (hls.js constructor) it is preferred; without either, the example falls
back to native `<video src>` (Safari/iOS) and shows a clear error in Chromium.

Tests: pure helpers are covered by `owncast/web/tests/youtubePlayerCore.test.ts` (Jest, 20
tests: transport selection, persistence, formatting, WHEP URL variants, YouTube stats-row layout
and N/A handling) and the UI is covered by `tools/browser/youtube-player-e2e.mjs` (140 checks:
mount/playback, control-bar fidelity and absence of a quality label, watermark, integrated LIVE
pill at the edge and behind-live click-to-live, settings main-menu/submenu structure, stats panel
geometry/labels/bars for both transports, video scaling at 360/720/1200px in WebRTC and HLS plus
fullscreen, HLS playback speed, keyboard shortcuts, PiP and console cleanliness). One command runs
the whole flow against a fresh instance with a production bundle built from a clean-HEAD temporary
worktree (the tracked `static/web` in the main checkout is never modified):

```bash
tools/scripts/run-player-e2e.sh            # build bundle + binary, start server, publish demo,
                                           # run E2E, print the report path
PLAYER_SKIP_BUNDLE=1 tools/scripts/run-player-e2e.sh   # reuse the existing test binary
```

Screenshots and the machine-readable report land in `artifacts/browser/`:
`youtube-player-default.png`, `youtube-player-settings-main.png`,
`youtube-player-settings-submenu.png`, `youtube-player-stats-webrtc.png`,
`youtube-player-stats-hls.png`, `youtube-player-behind-live.png`,
`youtube-player-live-head.png`, `youtube-player-fullscreen.png`,
`youtube-player-auto-webrtc.png`, `youtube-player-scaling-{1200,720,360}-{webrtc,hls}.png` and
`youtube-player-report.json`. Acceptance reference: `docs/design/youtube-player-reference.md`
and `artifacts/reference/youtube-player/`. Tested in Chromium 158 via Playwright; other browsers
are untested.

Known limits: playback speed is HLS-only (WebRTC real-time); WebRTC quality changes establish a
new WHEP session (brief rebuffer); the cast affordance is omitted because casting is unsupported;
HLS fps/codec details show N/A when VHS does not expose them (resolution/bitrate/buffer/level are
real); WebRTC buffer health/live latency are N/A (not observable client-side); the server viewer
count can be N/A when `/api/status` omits `viewerCount` at poll time; fullscreen depends on the
browser's Fullscreen API.

