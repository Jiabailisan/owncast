# Media Pipeline Audit - Batch 1 Remediation Log

- Base branch: `feat/srt-webrtc-develop`
- Base audited SHA: `f799bb305b1e3f74e6145e825aa26269c87e035b`
- Scope: Batch 1 fixes (`#1`, `#2`, `#3`, `#12`, `#14`) from the full live media pipeline and player static audit

## 1. Fixed Items

### #1. Shared mode independent WebRTC transcode legs lost audio RTP output
- **Root cause**: In `services/transcoder/sharedOutput.go` (`sharedTeeSlaves`), when `sharedOutputEnabled()` was true and a non-copy WebRTC leg was absent from `plan`, the loop executed `continue` before appending the shared Opus audio RTP slave (`output.AudioURL`). `appendIndependentLegOutputs` only rendered video flags (`rtpVideoOutputFlags`), leaving independent transcode legs completely silent.
- **Fix**: Removed the premature `continue` in `sharedTeeSlaves` so only the video tee slave is gated on `plan[legIndex]`, while every non-video-only leg (shared, independent transcode, and copy) receives the shared Opus audio RTP slave from the tee.
- **Files**: `services/transcoder/sharedOutput.go`, `services/transcoder/sharedOutput_test.go`.

### #2. RTP 32-bit timestamp wrap-around permanently froze WebRTC playback
- **Root cause**: In `services/webrtc/normalize.go` (`normalizer.apply`), `isDiscontinuity` correctly used signed modular arithmetic (`int32(ts - n.lastInTS)`), but the monotonicity clamp in the `default` branch used unsigned comparison (`pkt.Timestamp < n.lastOutTS`). Once a 32-bit RTP timestamp wrapped past `0xFFFFFFFF` to small values, every subsequent packet was permanently clamped to `n.lastOutTS`.
- **Fix**: Changed the monotonicity clamp to signed modular difference `int32(pkt.Timestamp-n.lastOutTS) < 0` so legitimate wrap-around advances cleanly while genuinely late reordered packets remain clamped.
- **Files**: `services/webrtc/normalize.go`, `services/webrtc/normalize_test.go`.

### #3. Shared encode reuse blocked single-leg streams and drifted on `constrained-high`
- **Root cause**:
  1. `sharedOutputEnabled()` and `sharedAudioEncoderFlags()` called `sharedAudioCompatible(t.rtpOutputs)`, which required `len(outputs) >= 2` (a constraint only needed for the legacy standalone audio tee in `sharedRTPAudioFlags`). Single-variant WebRTC streams could never reuse an HLS video encode.
  2. `normalizeSharedH264Profile` in `services/transcoder/transcoder.go` mapped `"constrained-high"` to `"main"`, while `normalizeH264Profile` in `services/transcoder/rtp.go` mapped it to `"high"`, causing profile comparison in `variantMatchesLeg` to fail whenever `constrained-high` was configured.
- **Fix**:
  1. Split `allLegsShareOpusSettings(outputs)` (`len >= 1`) from `sharedAudioCompatible(outputs)` (`len >= 2 && allLegsShareOpusSettings`), and wired `sharedOutputEnabled()` / `sharedAudioEncoderFlags()` to `allLegsShareOpusSettings`.
  2. Delegated `normalizeSharedH264Profile` directly to `normalizeH264Profile`.
- **Preserved invariants**: Per `docs/design/webrtc-output-plan.md` and `services/transcoder/sharedOutput_test.go`, the exact rate-control match (`variant.getAllocatedVideoBitrate() == output.VideoBitrateKbps`, keeping the v0.3.1 golden HLS argv untouched) and exact GOP equality check (`variant.framerate * SecondsPerSegment == output.GOPFrames`) are intentionally preserved so a mismatched HLS encode is never silently substituted.
- **Files**: `services/transcoder/rtp.go`, `services/transcoder/sharedOutput.go`, `services/transcoder/transcoder.go`, `services/transcoder/sharedOutput_test.go`.

### #12. HLS fullscreen hid custom `PlayerControls` and `StatsPanel`
- **Root cause**: `OwncastPlayer.toggleFullScreen()` called `playerRef.current.requestFullscreen()`, which placed video.js's inner `.video-js` element into fullscreen. Because `<PlayerControls>` (including `<StatsPanel>`) renders as a sibling outside `.video-js` and native video.js controls are disabled (`controls: false`), entering fullscreen hid all controls and stats.
- **Fix**: Changed `toggleFullScreen()` in `OwncastPlayer.tsx` to request fullscreen on the outer `#player` container (`containerRef.current`) with `webkitRequestFullscreen` and iOS Safari `<video>.webkitEnterFullscreen` fallbacks, synced `fullscreen` state via `document` `fullscreenchange`/`webkitfullscreenchange` plus WebKit video fullscreen events, and added `:fullscreen` sizing rules in `OwncastPlayer.module.scss` (using `object-fit: contain` in fullscreen).
- **Files**: `web/components/video/OwncastPlayer/OwncastPlayer.tsx`, `web/components/video/OwncastPlayer/OwncastPlayer.module.scss`, `web/tests/owncastPlayerFullscreen.test.tsx`.

### #14. `WebRTCPlayer` had no runtime ICE/connection drop recovery
- **Root cause**: After WHEP negotiation resolved, neither `whep-client.ts` nor `WebRTCPlayer.tsx` listened to `pc.iceconnectionstatechange`, `pc.connectionstatechange`, or remote video `track.ended`. Mid-stream network switches, NAT timeouts, or server-side variant withdrawals left the player frozen on the last frame with no reconnect, error, or HLS fallback.
- **Fix**: Added runtime listeners in `WebRTCPlayer.tsx` for `iceconnectionstatechange`, `connectionstatechange`, and remote video track `ended` (with a 5 s grace period on `disconnected` before triggering recovery, 1 automatic reconnect attempt, and clean teardown + `onFallback` / error UI if recovery fails). Listeners are detached before teardown so normal unmount and quality switches never trigger false disconnects.
- **Files**: `web/components/video/webrtc/WebRTCPlayer.tsx`, `web/tests/webRTCPlayer.test.tsx`.

## 2. Verification Results

All checks were executed using workspace-local toolchains (`.toolchains/`):

- **Go (`go1.26.2 linux/amd64`)**:
  - `gofmt -l` on all modified Go files: clean.
  - `go vet ./services/transcoder/... ./services/webrtc/...`: passed.
  - `go test -count=1 ./services/transcoder/... ./services/webrtc/...`: passed (`services/transcoder` 2.64s, `services/webrtc` 10.06s; run without `-race` because `gcc` is absent in this environment).
- **Web (`node v24.22.2`, `jest 30.4.2`)**:
  - `npm run typecheck` (`tsc --noEmit`): passed (0 errors).
  - `eslint` on modified player/test files: passed (0 errors).
  - `stylelint` on `OwncastPlayer.module.scss`: passed (0 errors).
  - `prettier --check` on all modified web files: passed.
  - `jest` on 8 player test suites (`webRTCPlayer`, `owncastPlayerFullscreen`, `playerContainer`, `playerControls`, `videojsDispose`, `webRTCLatencyCatchUp`, `webrtcStats`, `whepClient`): **8/8 suites passed, 89/89 tests passed**.

## 3. Remaining Audit Batches (Not Yet Applied)

- **Batch 2 (Ingest startup & index consistency)**: `#4` (`awaitSourceBitrate` idle wait), `#5` (Isolated 32 MiB vs 4 MiB overflow), `#6` (`webrtc.Service.Variants()` sparse map), `#7` (`ParseSPS` 8x8 scaling list), `#8` (`effectiveWebRTCVariants` shallow copy), `#9` (HLS index renumbering & `OwncastPlayer` quality lookup), `#10` (RTMP `setCurrentBroadcasterInfo` lockless overwrite).
- **Batch 3 (Stats panel & admin observability)**: `#11` (VHS `vhs.stats.*` & Auto representation), `#13` (WebRTC `estimatedPlayoutTimestamp` NTP epoch & selected ICE RTT), `#15` (HLS `clockSkew`), `#20` (admin status & FFmpeg panel accuracy), `#21`-`#25` (WebRTC sparkline zero spike, StatsPanel fields/UX, WebRTC `.bigUnmuteButton` positioning, `LatencyCompensator` manual quality lock, `PlaybackMetrics` global XHR cleanup & embed `streamIdentity`).
- **Batch 4 (Robustness & edge cases)**: `#16`-`#19`, `#26`-`#27`.
