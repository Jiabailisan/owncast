# Full-Repo Audit & UX/Performance Remediation Log

Date: 2026-11-26
Branch: `feat/srt-webrtc-develop`

## Overview

Following the four media-pipeline remediation batches (`f4cba52`, `026fc10`, `75431ae`, `f51deb0`), Review 2 (`6db67df`), and root-cause resolution of the Next.js `16.4.6` Turbopack CSP inline-script boot block (`web/node_modules` resynced to lockfile `next@16.2.10`), a comprehensive full-repository audit was performed across backend services, HTTP/WebSocket routing, frontend boot/runtime state, player UX, and CI build reproducibility.

## Commits & Remediation Summary

### Batch A (`a49af6a`): Backend CPU/GPU Performance, HTTP/Plugin Security & Graceful Shutdown

- **GPU NVENC Hardware Scaling (`services/transcoder/codecs.go`, `transcoder.go`)**:
  - Added cached `ffmpeg -hide_banner -filters` probe for `scale_cuda`.
  - When available, `NvencCodec` now runs `-hwaccel cuda -hwaccel_output_format cuda`, `scale_cuda`, and `cuda` pixel format, avoiding full-frame GPU->CPU->GPU download/scale/upload on NVENC hosts.
- **MPEG-TS Inspector O(N^2) -> O(N) & Probe Short-Circuit (`services/ingest/mpegts/inspector.go`, `bitrate.go`)**:
  - Added `esScanPos` so Annex-B start-code scanning no longer re-scans the entire accumulated buffer on every 188-byte TS packet.
  - Stopped buffering/scanning `es` and `audioES` once SPS/B-frame and audio metadata probes complete, retaining O(1) PES bitrate accounting.
- **HTTP Compression & Static/Template Caching (`webserver/router/router.go`, `static/static.go`, `webserver/handlers/web.go`, `index.go`, `images.go`, `handlers.go`)**:
  - Restricted `httpcompression` to text-only MIME whitelist (`blacklist = false`) and bypassed compression for `.ts` segments, `/public/*`, and `Range` requests.
  - Cached compiled `index.html` / `metadata.html.tmpl` templates with `sync.Once`.
  - Added weak startup `ETag` + `304 Not Modified` and immutable `Cache-Control` for `/_next/static/chunks/`.
  - Hardened `isIndexRequest` (strict `/` and `/index.html`), 16-byte CSP nonce, `charset=utf-8`, `/admin/*` `private, no-store` headers, bot metadata cache isolation, and thumbnail cache TTL/error guards.
- **Plugin, Chat, Offline Cleanup & Graceful Shutdown (`services/plugins/*`, `services/chat/server.go`, `services/stream/*`, `main.go`)**:
  - Stripped `Authorization` from plugin request envelopes and `owncast_admin_session` cookies from plugin responses; moved `FunctionExists` checks under `p.mu`.
  - Wrapped `/api/plugins/*` routes with `chiMW.Recoverer` + `viewerAuthGate`.
  - Removed racy `upgrader.CheckOrigin` mutation in chat handler; moved 7s welcome delay off the read path.
  - Fixed `offlineTmpFile` fd leak/nil guards, `TempDir` thumbnail/preview cleanup paths, and `offlineCleanupMu` locking.
  - Wired `signal.NotifyContext(os.Interrupt, syscall.SIGTERM)` into `router.StartWithContext` and removed invalid `-webserverport` early exit.

### Batch B (`1397ecb`): Unified Admin Outbound Streams with HLS Status & WebRTC Audio Bitrate

- **Backend Telemetry (`services/webrtc/forwarder.go`, `services/transcoder/hlsHandler.go`, `services/stream/hls_metrics.go`, `services/stream/webrtc_legs.go`, `services/livestatus/service.go`, `models/liveStatus.go`)**:
  - Recorded WebRTC RTP audio packet bitrate (`AudioBitrateKbps` -> `OutputAudioBitrateKbps`) and populated `AudioEncoder: "libopus"` on active WebRTC legs.
  - Added per-variant EWMA HLS measured segment bitrate tracking (`HLSMeasuredBitrates()`) via `HLSHandler.OnSegmentWritten`, exposed via `LiveOutboundStream.HLS`.
- **Admin UI (`web/components/admin/live/OutboundStreamsTable.tsx`, `LiveStreamStatusSection.tsx`)**:
  - Merged the overlapping `Outbound streams` and `WebRTC outputs` sections into a single unified `Outbound streams` table displaying variant metadata, HLS status + measured bitrate, and WebRTC requested/applied/source/encoder/audio (`libopus` + measured audio bitrate)/measured video bitrate/notes alongside the WHEP advertised `profile-level-id` banner.

### Batch C: Player UX, Frontend Boot/State Hardening & CSP Build Gate

- **Unmute Pill Tracks Control Bar Visibility (`PlayerControls.tsx`, `OwncastPlayer.tsx`, `VideoJS.scss`, `WebRTCPlayer.tsx`, `WebRTCPlayer.module.scss`)**:
  - Added `onControlsVisibilityChange` to `PlayerControls` and aligned both HLS and WebRTC Unmute pills to smoothly transition (`bottom 0.2s ease`) down to `max(14px, env(safe-area-inset-bottom))` when the control bar auto-hides.
- **WebRTC `Minimize latency` Optimization (`WebRTCPlayer.tsx`)**:
  - Replaced artificial `jitterBufferTarget = 200` ms / `playoutDelayHint = 0.2` s floor with `0` when enabled (`null` when disabled).
  - Lowered catch-up thresholds to stepped `1.05x` (`> 0.5s`) and `1.1x` (`> 1.0s`), releasing below `0.3s`, and re-applying `0` on `visibilitychange` return to foreground.
- **HLS `Connection Speed` Smoothing (`PlayerControls/stats.ts`, `OwncastPlayer.tsx`)**:
  - Removed `Math.max(measured, systemKbps)` peak-pinning, ignored sub-5ms quantized segment download durations when `systemBandwidth` is available, and applied EWMA smoothing (`alpha = 0.35`).
- **Frontend Boot, Service Worker, State & UI Fixes**:
  - Narrowed Workbox `dontCacheBustURLsMatching` to `/^\/_next\/static\/chunks\//` and imported `/serviceWorker.js` into `/sw.js` so a single root-scoped worker handles precaching and Web Push without evicting each other.
  - Added `response.ok` checks to `client-config-service.ts` and `status-service.ts`.
  - Made `serverFailure` recoverable and added `OFFLINE` transition on `chatUserDisabled` in `application-state.ts`.
  - Fixed `ClientConfigStore.tsx` (`wsRef`, reactive `hasWebsocketDisconnectedAtom`, live `appStateService.getSnapshot()`, automatic `/api/config` refresh on `offline -> online` transition and boot recovery, unmount WebSocket cleanup).
  - Guarded empty `availableBitrates` reduce in `stream-health.tsx`, fixed single non-About tab rendering in `DesktopContent.tsx`, and fixed duplicate `supportsBrowserNotifications` check in `ActionButtons.tsx`.
- **Reproducible CSP-Safe Static Bundle (`build/web/bundleWeb.sh` + refreshed `static/web/`)**:
  - Switched `bundleWeb.sh` from `npm install` to `npm ci` and added a post-export CSP gate rejecting any un-nonced executable inline `<script>` tags across `out/**/*.html`.
  - Regenerated and committed `static/web/` so GitHub's `Static web bundle / Committed bundle matches a fresh build` check passes.

## Note on OBS "Dropped Frames (Network)" (`0/xxxxx`)

Both `joy5` (RTMP `WindowAckSize`/`SetPeerBandwidth`/`Ack`) and `gosrt@v0.13.0` (SRT `ACK`/`ACKACK`/`NAK`/`TSBPD`/`TLPKTDROP`) implement protocol-level loss/flow feedback normally. OBS Studio's `Dropped Frames (Network)` counter strictly measures **frames dropped inside OBS's own outbound send buffer/queue** when the link cannot drain frames fast enough (TCP backpressure or SRT send buffer saturation); it intentionally does not increment from transient receiver-side packet losses that are recovered via SRT ARQ/retransmission or below the send-queue overflow threshold.
