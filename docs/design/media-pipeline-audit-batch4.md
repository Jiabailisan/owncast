# Media Pipeline Audit - Batch 4 Remediation Log

- Base branch: `feat/srt-webrtc-develop`
- Base commit SHA: `75431aebbf23edaa7a6c536b2f54d6e8c31447e6` (Batch 3 commit; original audit baseline `f799bb305b1e3f74e6145e825aa26269c87e035b`)
- Scope: Final Batch 4 fixes (`#16`, `#17`, `#18`, `#19`, `#26`, `#27`) completing all 27 findings from the full live media pipeline and player static audit.

## 1. Fixed Items

### #16. `ingestTee` aligner byte ledger drift, `endCh` bypassing aligner, FLV boundary validation, and video-only FLV header flags
- **Root cause**:
  1. `teeBranch.pump()` subtracted `len(aligned)` from `b.pending` after `feedAligner(chunk)` rewrote the chunk (inserting FLV prefix or trimming partial head bytes), causing `b.pending` to drift away from actual queued bytes.
  2. The `case <-b.endCh:` drain loop wrote remaining queued chunks directly to `b.writer`, bypassing `feedAligner`.
  3. `flvTagBoundary` checked only two 11-byte tag headers without verifying that the trailing 4-byte `PreviousTagSize` equaled `11 + dataSize`, permitting false-positive mid-payload matches.
  4. `rtmp.session.ContainerPrefix()` hardcoded FLV header type flags `0x05` (video+audio) even for video-only streams.
- **Fix**:
  - Unified normal and `endCh` writes in `teeBranch.pump()` through a single `writeAligned` helper that strictly subtracts the raw dequeued `int64(len(chunk))` from `b.pending` while counting `len(aligned)` in `b.bytesOut`.
  - Added `PreviousTagSize == 11 + dataSize` verification in `flvTagBoundary`.
  - Computed FLV header type flags dynamically in `ContainerPrefix()` (`0x01` if video config present, `0x04` if audio config present).
- **Files**: `services/stream/ingest_tee.go`, `services/stream/batch4_test.go`, `services/rtmp/source_metadata.go`, `services/rtmp/source_metadata_test.go`.

### #17. Shared mode `writeMasterPlaylist` bypassed storage rewrite hook and `SetSharedH264Level` was never called
- **Root cause**: `Transcoder.writeMasterPlaylist()` wrote `stream.m3u8` directly to disk without notifying `FileWriterReceiverService`'s `MasterPlaylistWritten` callback (skipping `rewritePlaylistLocations` for external CDN/`VideoServingEndpoint` deployments), and `services/stream` only called `SetSharedH264Profile` while leaving `SetSharedH264Level` unset.
- **Fix**:
  - Added `SetMasterPlaylistWrittenCallback` on `Transcoder` and invoked it from `writeMasterPlaylist()` after writing `stream.m3u8`.
  - Wired `tr.SetSharedH264Level(webrtcCfg.H264Level)` and `tr.SetMasterPlaylistWrittenCallback(s.handler.MasterPlaylistWritten)` in `services/stream/stream.go` (and `SetSharedH264Level` in `services/stream/isolated.go`).
- **Files**: `services/transcoder/transcoder.go`, `services/transcoder/sharedOutput.go`, `services/transcoder/batch4_test.go`, `services/stream/stream.go`, `services/stream/isolated.go`.

### #18. Video passthrough + audio transcode produced invalid FFmpeg flag `-b:a:0 ""`
- **Root cause**: `getVariantFromConfigQuality` returned early when `variant.isVideoPassthrough` was true, before calling `variant.SetAudioBitrate(strconv.Itoa(quality.AudioBitrate) + "k")`, leaving `variant.audioBitrate` empty when audio transcoding was requested alongside video passthrough.
- **Fix**: Set `variant.SetAudioBitrate(...)` before the `isVideoPassthrough` early return whenever `quality.AudioBitrate > 0 && !variant.isAudioPassthrough`.
- **Files**: `services/transcoder/transcoder.go`, `services/transcoder/batch4_test.go`.

### #19. WebRTC config/fallback, default jitter buffer delay, and structured auto-downgrade
- **Root cause**:
  1. `PlayerContainer` ignored `webrtcConfig.fallbackToHls === false`, silently switching failed WebRTC sessions to HLS.
  2. `/api/config` (`buildPublicWebRTCConfig`) advertised every planned variant from `broadcast.OutputSettings` even after legs failed to open or were disabled/withdrawn at runtime.
  3. `WebRTCPlayer` set `jitterBufferTarget = 1000` / `playoutDelayHint = 1` when `Minimize latency` was off, injecting 1 second of artificial delay into WebRTC playback.
  4. Auto packet-loss downgrade guessed passthrough vs encoded solely from variant names and did not verify `session.variantIndex !== encodedVariant.index`.
- **Fix**:
  - Updated `PlayerContainer.tsx` to respect `webrtcConfig.fallbackToHls === false` (disabling `sessionFallback` override and omitting `onFallback`, keeping `WebRTCPlayer`'s error/retry UI mounted).
  - Updated `buildPublicWebRTCConfig` in `webserver/handlers/config.go` to filter variants against live `h.stream.WebRTCLegs()` (excluding `disabled` legs, including legs that failed to open via `markWebRTCLegsDisabledForGeneration` in `services/stream/webrtc.go`) and emit additive `isPassthrough`.
  - Restored browser default `null` for `jitterBufferTarget` / `playoutDelayHint` when `latencyEnabled === false` in `WebRTCPlayer.tsx`.
  - Updated `chooseEncodedWebRTCVariant` to prefer structured `variant.isPassthrough === false` (with regex fallback for older servers) and prevented `WebRTCPlayer` auto-downgrade from reconnecting onto the currently active or already-encoded variant.
- **Files**: `services/stream/webrtc.go`, `webserver/handlers/config.go`, `webserver/handlers/config_batch4_test.go`, `web/interfaces/client-config.model.ts`, `web/components/video/PlayerContainer.tsx`, `web/components/video/webrtc/webrtc-stats.ts`, `web/components/video/webrtc/WebRTCPlayer.tsx`, `web/tests/playerContainer.test.tsx`, `web/tests/webrtcStats.test.ts`.

### #26. Backend concurrency & protocol edge cases
- **Root cause**:
  1. Unlocked read of `webRTCVariantGen` in `isolated.go` (already resolved in Batch 2 by passing `webrtc.variantGen` from `configureWebRTC`; verified zero remaining unlocked reads across `services/stream`).
  2. In `services/ingest/mpegts/bitrate.go`, `consumeVideoPayload` and `consumeAudioPayload` returned early on `len(es) == 0` before calling `pesAccumulator.addPacket(..., payloadStart)`, so an empty-ES `PUSI=1` header packet failed to flush the previous PES or arm the new PTS for continuation packets.
  3. In `services/ingest/flv/inspector.go`, `timeDeltaMs` used unsigned subtraction `int64(a - b)`, so a tiny backward DTS jitter (e.g. 20 ms) wrapped to ~49 days and evicted the entire bitrate window.
  4. In `services/webrtc/whep.go`, `serveSession` returned `503` when `!cfg.Enabled`, preventing existing viewers from cleanly closing sessions via `DELETE` (or completing trickle `PATCH`) after WebRTC was toggled off.
- **Fix**:
  - Called `addPacket` before the `len(es) == 0` early return and preserved `pts`/`hasPTS` on `payloadStart` even when `esBytes == 0`.
  - Switched `flv.timeDeltaMs` to signed 32-bit modular arithmetic `int64(int32(a - b))`, clamping mild backward jitter (`<= 1000 ms`) to the monotonic watermark while resetting cleanly on large backward clock resets (`> 1000 ms`).
  - Removed the `!cfg.Enabled` gate in `whepHandler.serveSession` so only new `POST` offers are rejected when disabled, while existing sessions can still `DELETE` / `PATCH`.
- **Files**: `services/ingest/mpegts/bitrate.go`, `services/ingest/mpegts/pes_empty_pusi_test.go`, `services/ingest/flv/inspector.go`, `services/ingest/flv/bitrate_jitter_test.go`, `services/webrtc/whep.go`, `services/webrtc/whep_disabled_delete_test.go`.

### #27. Frontend player null guards, relative URL cachebuster, and WebRTC heartbeat quality match
- **Root cause**:
  1. `OwncastPlayer.tsx` control callbacks and volume hotkeys (`0`/`9`) dereferenced `playerRef.current` without null checks before player readiness.
  2. `VideoJS.tsx` constructed cachebuster URLs via `new URL(uri, window.location.protocol + window.location.host)`, missing `//` (throwing `TypeError` on relative `.m3u8` URLs) and dropping subpath prefixes.
  3. `WebRTCPlayer.tsx` heartbeat fell back directly to substring matching `variant.name.includes(String(height))` before checking `activeVariantNameRef.current` or structured `variant.height`.
- **Fix**:
  - Added `playerRef.current` null guards in `setSavedVolume`, `handleVolume`, `togglePlayback`, `toggleMute`, `setPlayerVolume`, and volume hotkeys `0`/`9` (clamping to `[0, 1]`).
  - Changed `VideoJS.tsx` cachebuster base URL to `window.location.href`.
  - Updated `WebRTCPlayer.tsx` heartbeat quality resolution to prefer `activeVariantNameRef.current`, then explicit selection, then structured `variant.height === height`, and only finally name substring fallback.
- **Files**: `web/components/video/OwncastPlayer/OwncastPlayer.tsx`, `web/components/video/VideoJS/VideoJS.tsx`, `web/components/video/webrtc/WebRTCPlayer.tsx`, `web/tests/videojsDispose.test.tsx`.

## 2. Verification Results

All checks were executed using workspace-local toolchains (`.toolchains/`):

- **Go (`go1.26.2 linux/amd64`, `CGO_ENABLED=0` because `gcc` is absent in this environment)**:
  - `gofmt -l` on all 18 modified/new Go files: clean.
  - `go vet ./services/stream/... ./services/rtmp/... ./services/transcoder/... ./services/ingest/mpegts/... ./services/ingest/flv/... ./services/webrtc/... ./webserver/handlers/...`: passed (0 issues).
  - `go test -count=1 ./services/rtmp/...`: passed (`0.005s`, including `TestRTMPContainerPrefixVideoOnlySetsVideoFlagOnly`).
  - `go test -count=1 ./services/transcoder/...`: passed (`2.591s`, including `TestVideoPassthroughWithAudioTranscodeSetsAudioBitrate` and `TestSharedWriteMasterPlaylistInvokesCallbackAndUsesSharedLevel`).
  - `go test -count=1 ./services/ingest/mpegts/...`: passed (`0.048s`, including `TestPESAccumulatorEmptyPayloadPUSIFlushesPreviousAndArmsPTS`).
  - `go test -count=1 ./services/ingest/flv/...`: passed (`0.002s`, including `TestFLVRateTrackerToleratesMildNonMonotonicDTSJitter`).
  - `go test -count=1 ./services/webrtc/...`: passed (`10.467s`, including `TestWHEPAllowsSessionDeleteWhenDisabled` and `TestEndToEndMediaDelivery`).
  - `go test -count=1 ./services/stream` (non-sqlite tests including `TestFLVTagBoundaryValidatesPreviousTagSize` and `TestIngestTeePendingLedgerStaysZeroAfterFLVAlignerAndEndChAligns`): passed (`1.201s`).
  - **Not executed due to missing `gcc`/CGO**: `go test -race` and SQLite3-backed `TestMain` execution in `webserver/handlers` (including `TestBuildPublicWebRTCConfigFiltersDisabledLegsAndSetsIsPassthrough`, which compiles and passes `go vet` cleanly).
- **Web (`node v24.22.2`, `jest 30.4.2`)**:
  - `npm run typecheck` (`tsc --noEmit`): passed (0 errors).
  - `eslint` on all modified/new TS/TSX files: passed (0 errors).
  - `prettier --check` on all modified/new Web files: passed.
  - `jest` on 7 suites (`playerContainer`, `webrtcStats`, `webRTCPlayer`, `webRTCLatencyCatchUp`, `videojsDispose`, `owncastPlayerFullscreen`, `playerControls`): **7/7 suites passed, 77/77 tests passed**.

## 3. Audit Completion Status

All 27 findings from the static audit of `feat/srt-webrtc-develop` (baseline `f799bb305b1e3f74e6145e825aa26269c87e035b`) are now remediated across four documented batches:
- Batch 1 (`f4cba52`): `#1`, `#2`, `#3`, `#12`, `#14`
- Batch 2 (`026fc10`): `#4`, `#5`, `#6`, `#7`, `#8`, `#9`, `#10`
- Batch 3 (`75431ae`): `#11`, `#13`, `#15`, `#20`, `#21`, `#22`, `#23`, `#24`, `#25`
- Batch 4 (this commit): `#16`, `#17`, `#18`, `#19`, `#26`, `#27`
