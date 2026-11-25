# Media Pipeline Audit - Batch 3 Remediation Log

- Base branch: `feat/srt-webrtc-develop`
- Base commit SHA: `026fc105edee2b0ee02df26f73932b5737701022` (Batch 2 commit; original audit baseline `f799bb305b1e3f74e6145e825aa26269c87e035b`)
- Scope: Batch 3 fixes (`#11`, `#13`, `#15`, `#20`, `#21`, `#22`, `#23`, `#24`, `#25`) covering HLS/WebRTC Stats for nerds accuracy, admin live status & FFmpeg observability, WebRTC unmute overlay positioning, HLS latency compensator manual quality lock respect, and playback metrics/lifecycle cleanup.

## 1. Fixed Items

### #11. HLS Stats panel misread `vhs.*` (instead of `vhs.stats.*`) and picked the first enabled rendition in Auto mode
- **Root cause**: `OwncastPlayer.tsx` read `vhs?.mediaBytesTransferred`, `vhs?.mediaSecondsLoaded`, and `vhs?.mediaTransferDuration` off the VHS root instead of `vhs?.stats.*`, computed lifetime averages rather than recent segment-completion deltas, and chose `activeRepresentation` via `representations.find(r => r.enabled())` (which always returns index 0 under Auto ABR).
- **Fix**:
  - Read cumulative counters from `vhs?.stats ?? vhs` and compute sliding segment-completion deltas in `hlsMeterRef` so bitrate and download speed reflect recent segments instead of a flattened session average.
  - Resolve `activeRepresentation` from `vhs?.playlists?.media?.()` and `activeVariantIndex(vhs)` before falling back to `.find(r => r.enabled())`, and read `BANDWIDTH` / `FRAME-RATE` / `CODECS` from the active media playlist attributes.
- **Files**: `web/components/video/OwncastPlayer/OwncastPlayer.tsx`, `web/components/video/PlayerControls/stats.ts`.

### #13. WebRTC Stats panel mixed NTP/Unix epochs on `estimatedPlayoutTimestamp` and detached `candidateRtt` from the selected ICE pair
- **Root cause**:
  1. `collectWebRTCStatsPanel` subtracted `video.estimatedPlayoutTimestamp` (NTP epoch milliseconds) directly from `Date.now()` (Unix epoch milliseconds), leaving a ~70-year offset that always failed the `0..30s` guard.
  2. `candidateRtt` in `collectWebRTCStatsPanel` and `summarizeWebRTCStats` overwrote itself with whichever `candidate-pair` appeared last in `RTCStatsReport` rather than reading from the exact pair chosen by `selectedIceTransport()`.
  3. `WebRTCPlayer` hardcoded `bufferSeconds: null` instead of exposing the receiver jitter buffer delay.
- **Fix**:
  - Extracted shared `selectedIceCandidatePair(report)` and wired `selectedIceTransport`, `summarizeWebRTCStats`, and `collectWebRTCStatsPanel` to read transport and RTT from that exact selected pair.
  - Converted `estimatedPlayoutTimestamp` from NTP epoch ms to Unix ms (`- 2_208_988_800_000`) when in NTP epoch range, while preserving the interval `jitterBuffer + processing + rtt/2` fallback.
  - Exposed interval `jitterBufferSeconds` on `WebRTCStatsPanelSample` and rendered it in `WebRTCPlayer`'s `Buffer Health` row.
- **Files**: `web/components/video/webrtc/webrtc-stats.ts`, `web/components/video/webrtc/WebRTCPlayer.tsx`, `web/tests/webrtcStats.test.ts`.

### #15. HLS Stats panel and `/api/ping` omitted `clockSkew`, and `hlsPlaylistSegments` skipped the public VHS API
- **Root cause**: `measureHlsLatencyMs` and the HLS `StatsPanel` builder called `hlsLiveLatencySeconds({ nowMs: Date.now(), ... })` without applying `clockSkewAtom`, causing divergence from `LatencyCompensator` and `PlaybackMetrics` on skewed client clocks. `hlsPlaylistSegments` also only inspected internal `playlistController_` / `masterPlaylistController_` fields.
- **Fix**:
  - Maintained a numeric `clockSkewRef` synced with `clockSkewAtom` (also forwarded to `latencyCompensatorRef` and `playbackMetricsRef`) and passed `nowMs: Date.now() + clockSkewRef.current` to both `measureHlsLatencyMs` and the HLS `StatsPanel` sampler.
  - Updated `hlsPlaylistSegments` to check public `vhs.playlists?.media?.()` first.
- **Files**: `web/components/video/OwncastPlayer/OwncastPlayer.tsx`, `web/components/video/PlayerControls/stats.ts`, `web/tests/hlsLatency.test.ts`.

### #20. Admin live status, FFmpeg panel, and WebRTC leg metrics diverged from runtime state
- **Root cause**:
  1. `livestatus.Service.Status()` and `Admin.GetLiveFFmpeg` reported configured `GetOutputProcessModel()` instead of the active stream's `GetRunningProcessModel()`.
  2. `Transcoder.state.restarts` was never updated by the isolated WebRTC supervisor (`superviseWebRTCProcess`), so `RestartCount` stayed `0`.
  3. `MediaLeg.Stats()` computed lifetime average FPS and UDP/RTP-header-inclusive bitrate since `startedAt` and never reset on `MarkRestart()`.
  4. `outboundStatus` ignored runtime WebRTC leg fallbacks, disabled/withdrawn legs, and measured leg bitrates; `withdrawWebRTCVariants` did not update `webRTCLegsStatus`.
  5. `StreamStatusCards` coerced absent process metrics to `0`, only read `ffmpeg.processes[0].speed`, and reused `ffmpegSpeed` as the `Processes` card title key; `CurrentVariantsTable` copy note falsely stated RTMP always falls back to transcoding.
- **Fix**:
  - Preferred `GetRunningProcessModel()` when non-empty in `services/livestatus/service.go` and `webserver/handlers/admin/live.go`.
  - Added `Transcoder.SetRestartCount(attempt)` and called it in `superviseWebRTCProcess`; updated `withdrawWebRTCVariants` to mark withdrawn legs `disabled` in `webRTCLegsStatus`.
  - Added `applyWebRTCLegsToOutbound` in `services/livestatus/service.go` to reconcile disabled/withdrawn legs, WebRTC-only transcode/Opus encoders, and measured leg bitrates.
  - Replaced lifetime average in `services/webrtc/forwarder.go` with a zero-allocation 5-second sliding window (`slidingRateWindow`) over RTP payload bytes and video frame markers, reset cleanly in `markRestart()`.
  - Updated `StreamStatusCards.tsx` to preserve `null` (`N/A`) when process metrics are absent, aggregate speeds across all running processes (e.g. `HLS 1.00x / WEBRTC 0.96x`), and use dedicated `Admin.LiveStreaming.Status.ffmpegProcesses` key; updated `copyNote` in `CurrentVariantsTable.tsx` and `web/i18n/en/translation.json`.
- **Files**: `services/livestatus/service.go`, `services/livestatus/batch3_test.go`, `services/transcoder/process.go`, `services/transcoder/process_restart_test.go`, `services/stream/isolated.go`, `services/webrtc/forwarder.go`, `services/webrtc/forwarder_rate_test.go`, `webserver/handlers/admin/live.go`, `web/components/admin/live/StreamStatusCards.tsx`, `web/components/admin/CurrentVariantsTable.tsx`, `web/types/localization.ts`, `web/i18n/en/translation.json`, `web/tests/liveStatusComponents.test.tsx`.

### #21. WebRTC Stats sparkline first-sample `0 kbps` spike, 1000 ms timer jitter FPS fallback, and stale curves on variant switch
- **Root cause**: Pushing `null` bitrates from the first `collectWebRTCStatsPanel` sample converted `null` into `0`, creating a false `0 kbps` drop; single-point sparklines rendered a full-width triangle `<polygon>`; `seconds >= 1` using `Date.now()` dropped measured FPS whenever a 1 s `setInterval` fired at 995 ms; and `WebRTCPlayer`'s stats effect omitted `selectedVariant`/`variants` deps and never cleared `sparklines`.
- **Fix**:
  - Skipped pushing `null` bitrates to `sparklines` in both `WebRTCPlayer` and `OwncastPlayer`, and cleared `sparklines` on session reconnect, variant switch, and panel reopen.
  - Rendered a centered `<circle>` marker for 1-point sparklines in `StatsPanel.tsx` and full `<polygon>` + `<polyline>` once `values.length >= 2`.
  - Preferred monotonic `RTCStats` `video.timestamp` deltas (falling back to `Date.now()` when unchanged/absent) and lowered the FPS window threshold to `>= 0.8s`.
  - Added `selectedVariant` and `variants` to `WebRTCPlayer`'s stats effect dependency list.
- **Files**: `web/components/video/webrtc/webrtc-stats.ts`, `web/components/video/webrtc/WebRTCPlayer.tsx`, `web/components/video/OwncastPlayer/OwncastPlayer.tsx`, `web/components/video/PlayerControls/StatsPanel.tsx`, `web/tests/webrtcStats.test.ts`, `web/tests/playerControls.test.tsx`.

### #22. `StatsPanel` viewport/codecs/placeholder inaccuracies, pinned control bar, Escape close, and mobile label overflow
- **Root cause**: `Viewport` used `window.innerWidth x window.innerHeight` instead of `<video>` rendered dimensions; `Optimal Res` duplicated `resolution`; HLS `Codecs` was hardcoded; `makeEmptyStats` mixed `'N/A'` with `'—'`; `statsOpen` was included in `anyMenuOpen` (pinning the control bar forever) and ignored `Escape`; and mobile `.statsChartLabel` was `34px` wide (clipping/overlapping `"Audio (ingest)"`).
- **Fix**:
  - Measured `Viewport` from `video.clientWidth x video.clientHeight` (with `window` fallback in headless environments) and `Optimal Res` from DPR-scaled player box pixels.
  - Formatted HLS `Codecs` from the active playlist's `CODECS` attribute via `formatHlsCodecs`, and normalized empty/unavailable panel placeholders to `'—'`.
  - Removed `statsOpen` from `anyMenuOpen` so the control bar auto-hides normally while the HUD stays open, and added `Escape` handling to close `StatsPanel` when no menu is open.
  - Widened mobile `.statsChartLabel` to `80px` with `overflow: hidden; text-overflow: ellipsis`.
- **Files**: `web/components/video/PlayerControls/types.ts`, `web/components/video/PlayerControls/stats.ts`, `web/components/video/PlayerControls/PlayerControls.tsx`, `web/components/video/PlayerControls/PlayerControls.module.scss`, `web/components/video/OwncastPlayer/OwncastPlayer.tsx`, `web/components/video/webrtc/WebRTCPlayer.tsx`, `web/tests/playerControls.test.tsx`.

### #23. `WebRTCPlayer` `.bigUnmuteButton` lacked absolute positioning and z-index
- **Root cause**: `WebRTCPlayer.module.scss` had duplicate `inset` declarations on `.bigUnmuteButton` and omitted `position: absolute`, `z-index`, `display: inline-flex`, `align-items`, and `cursor: pointer`, causing `<video>` to occlude the button.
- **Fix**: Removed the duplicate `inset` rule and added `position: absolute; z-index: 6; display: inline-flex; align-items: center; cursor: pointer;`.
- **Files**: `web/components/video/webrtc/WebRTCPlayer.module.scss`.

### #24. HLS `LatencyCompensator` overrode manual user quality lock
- **Root cause**: `setQualityPinned(true)` unconditionally disabled all renditions except the lowest (or above current), and `setQualityPinned(false)` unconditionally re-enabled every rendition—breaking a viewer's explicit manual quality selection.
- **Fix**: Added `isRepresentationEnabled` and `pinnedQualitySnapshot` in `latencyCompensator.js`. When `<= 1` rendition is enabled (manual lock), pinning is skipped; when pinning in Auto mode, the pre-pin enabled states are snapshotted and restored verbatim on stop.
- **Files**: `web/components/video/latencyCompensator.js`, `web/tests/latencyCompensatorPin.test.ts`.

### #25. `PlaybackMetrics` leaked global `videojs.Vhs.xhr` + bare `player.off()`, `OwncastPlayer` leaked module globals & `isVideoPlayingAtom`, and `/embed/video` omitted `streamIdentity`
- **Root cause**: `PlaybackMetrics` overwrote global `videojs.Vhs.xhr` without restoring it on `stop()` and called bare `this.player.off()`; `OwncastPlayer` stored `playbackMetrics` and `latencyCompensator` in module-level globals and did not reset `isVideoPlayingAtom` on unmount; `/embed/video` did not pass `streamIdentity` to `PlayerContainer`; and `PlayerContainer.handleWebRTCFallback` called `onProtocolChange('hls')` even when `transportPreference === 'webrtc'`.
- **Fix**:
  - Restored `videojs.Vhs.xhr` in `PlaybackMetrics.stop()` when still pointing to this instance's wrapper, and detached only PlaybackMetrics's own 8 listeners.
  - Moved `OwncastPlayer`'s `playbackMetrics` and `latencyCompensator` state into component refs and reset `setVideoPlaying(false)` + `ping.stop()` on unmount.
  - Passed `streamIdentity={lastConnectTime ? String(lastConnectTime) : ''}` in `web/pages/embed/video/index.tsx`.
  - Guarded `onProtocolChange?.('hls')` in `PlayerContainer.tsx` on `transportPreference !== 'webrtc'` and added `streamIdentity` to `handleTransportChange` deps.
- **Files**: `web/components/video/metrics/playback.js`, `web/components/video/OwncastPlayer/OwncastPlayer.tsx`, `web/pages/embed/video/index.tsx`, `web/components/video/PlayerContainer.tsx`, `web/tests/playbackMetrics.test.ts`.

## 2. Verification Results

All checks were executed using workspace-local toolchains (`.toolchains/`):

- **Go (`go1.26.2 linux/amd64`, `CGO_ENABLED=0` because `gcc` is absent in this environment)**:
  - `gofmt -l` on all 8 modified/new Go files: clean.
  - `go vet ./services/livestatus/... ./services/transcoder/... ./services/stream/... ./services/webrtc/... ./webserver/handlers/admin/...`: passed (0 issues).
  - `go test -count=1 ./services/livestatus/...`: passed (`0.007s`, including `TestApplyWebRTCLegsToOutboundReconcilesDisabledAndTranscodeFallback`).
  - `go test -count=1 ./services/transcoder/...`: passed (`2.607s`, including `TestTranscoderSetRestartCountReflectedInSnapshot`).
  - `go test -count=1 ./services/webrtc/...`: passed (`10.159s`, including `TestSlidingRateWindowMeasuresPayloadAndResetsOnRestart` and `TestEndToEndMediaDelivery`).
  - `go test -count=1 ./services/stream` (non-sqlite tests including `TestFFmpegPanel` and Batch 2 tests): passed (`1.217s`).
  - **Not executed due to missing `gcc`/CGO**: `go test -race` and SQLite3-backed tests in `webserver/handlers/admin` (which compiles and passes `go vet` cleanly).
- **Web (`node v24.22.2`, `jest 30.4.2`)**:
  - `npm run typecheck` (`tsc --noEmit`): passed (0 errors).
  - `eslint` on all modified/new TS/TSX/JS files: passed (0 errors).
  - `stylelint` on `PlayerControls.module.scss` and `WebRTCPlayer.module.scss`: passed (0 errors).
  - `prettier --check` on all modified/new Web files: passed.
  - `jest` on 14 suites (`webrtcStats`, `statsRound16`, `hlsLatency`, `playerControls`, `playerContainer`, `owncastPlayerFullscreen`, `webRTCPlayer`, `webRTCLatencyCatchUp`, `playbackMetrics`, `latencyCompensatorPin`, `liveStatusComponents`, `currentVariantsTable`, `localization-keys`, `videojsDispose`): **14/14 suites passed, 191/191 tests passed**.

## 3. Remaining Audit Batch (Not Yet Applied)

- **Batch 4 (Robustness & edge cases)**: `#16` (`ingestTee` aligner byte ledger, `endCh` aligner, FLV `PreviousTagSize` / `ContainerPrefix` flags), `#17` (Shared `writeMasterPlaylist` storage hook & `SetSharedH264Level`), `#18` (video passthrough + audio transcode `-b:a:0 ""`), `#19` (WebRTC `fallbackToHls=false`, runtime `/api/config` variants, `latencyEnabled=false` jitter buffer default, structured `isPassthrough` auto-downgrade), `#26` (backend concurrency & protocol edge cases), `#27` (frontend player null guards, relative URL cachebuster, WebRTC heartbeat height match).
