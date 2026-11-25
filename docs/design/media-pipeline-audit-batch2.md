# Media Pipeline Audit - Batch 2 Remediation Log

- Base branch: `feat/srt-webrtc-develop`
- Base commit SHA: `f4cba52d23950ed97c5756a085a31df90dc80dc8` (Batch 1 commit; original audit baseline `f799bb305b1e3f74e6145e825aa26269c87e035b`)
- Scope: Batch 2 fixes (`#4`, `#5`, `#6`, `#7`, `#8`, `#9`, `#10`) covering ingest startup read-ahead, Isolated tee buffering, H.264 SPS parsing, RTMP metadata concurrency, WebRTC variant planning/listing, and HLS variant index alignment.

## 1. Fixed Items

### #4. `awaitSourceBitrate` idled with no reader consuming `session.Reader()`
- **Root cause**: After `awaitSourceInfo` stopped its `prefetchReader` upon receiving a B-frame verdict, `configureWebRTC` called `awaitSourceBitrate(session)` before FFmpeg started. Because SRT/MPEG-TS (and backpressured pipes) only inspect bytes as `session.Reader()` is read, polling `session.Broadcaster()` for 2.5 s with no active reader never produced a measured bitrate and always fell back to the 2500 kbps default.
- **Fix**:
  - Updated `configureWebRTC` and `awaitSourceBitrate` to take `mediaReader *io.Reader`.
  - When `VideoBitrate == 0`, `awaitSourceBitrate` wraps `*mediaReader` in a bounded `prefetchReader` capped at the shared `webRTCReadAheadMaxBytes` (32 MiB) ceiling, updates `*mediaReader`, stops read-ahead as soon as bitrate lands or the wait returns, and exits early if `<-budgetSpent` fires (or if the ceiling was already reached during `awaitSourceInfo`).
  - Added `BufferedBytes()` (recursively summing nested `prefetchReader` buffers) and released the underlying slice (`p.buf = nil`) once drained in `prefetchReader.Read` so wrapping a prior `prefetchReader` frees its drained head and continues reading fresh bytes up to 32 MiB.
- **Files**: `services/stream/prefetch_reader.go`, `services/stream/webrtc.go`, `services/stream/stream.go`, `services/stream/batch2_test.go`.

### #5. Isolated mode Attempt 0 overflowed on prefetched heads > 4 MiB
- **Root cause**: `prefetchReader` can legitimately buffer up to 32 MiB during startup probing, while `ingestTee` buffered up to 4096 x 32 KiB (~128 MiB) in the critical `hls` branch channel and only 4 MiB in the optional `webrtc` branch. When `tee.Start()` replayed a > 4 MiB prefetched head immediately into `hls`'s huge channel while the isolated WebRTC FFmpeg process was still spawning, `webrtc` hit `errTeeBranchOverflow` on Attempt 0 and suffered a spurious restart.
- **Fix**:
  - Bounded critical tee branch channels to `teeCriticalBranchQueueChunks = 32` (~1 MiB) so the critical HLS branch exerts real backpressure on `fanOut` until FFmpeg starts reading.
  - Added per-branch `byteBudget` / `chunkCap` and `AddLateBranchWithBudget(..., extraByteBudget)` on `ingestTee`.
  - Sized Attempt 0's `webrtc` branch in `runIsolatedTranscoders` with `extraByteBudget = bufferedPrefetchBytes(mediaReader)` (capped at `webRTCReadAheadMaxBytes`) so Attempt 0 absorbs the prefetched head without spurious overflow while subsequent retries retain the standard 4 MiB budget.
  - Also passed `webrtc.variantGen` directly from `configureWebRTC` into `runIsolatedTranscoders` instead of reading `s.webRTCVariantGen` without `s.webRTCMu`.
- **Files**: `services/stream/ingest_tee.go`, `services/stream/isolated.go`, `services/stream/webrtc.go`, `services/stream/batch2_test.go`.

### #6. `webrtc.Service.Variants()` dropped sparse high-index variants
- **Root cause**: `Service.Variants()` iterated `for index := 0; index < len(s.variants); index++` over a `map[int]*variantTracks` keyed by configured variant index. When variant `0` disabled WebRTC (or was disabled via `DisableVariants`) while higher-index variants remained active, `len(s.variants)` was smaller than those keys, causing `Variants()` to return fewer variants (or an empty slice) and `Warnings()` to spuriously emit `webrtc_no_variants`.
- **Fix**: Collected actual map keys from `s.variants`, sorted them in ascending order with `slices.Sort`, and appended `s.variants[index].variant`.
- **Files**: `services/webrtc/service.go`, `services/webrtc/variants_sparse_test.go`.

### #7. `ParseSPS` skipped 8x8 scaling lists with length 16 instead of 64
- **Root cause**: In `services/ingest/mpegts/sps.go`, the `seq_scaling_matrix_present_flag` loop called `skipScalingList(r, 16)` for all `i < count` (8 or 12), ignoring ITU-T H.264 Table 7-2 where indices `0..5` are 4x4 (16 entries) and `6..11` are 8x8 (64 entries). High Profile streams carrying custom 8x8 scaling lists desynchronized the bit reader and failed or misparsed resolution/VUI fields.
- **Fix**: Passed `size := 16` for `i < 6` and `size = 64` for `i >= 6` to `skipScalingList`.
- **Files**: `services/ingest/mpegts/sps.go`, `services/ingest/mpegts/sps_scaling_test.go`.

### #8. `effectiveWebRTCVariants` shallow-copied pointers and mutated `broadcast.OutputSettings` in place
- **Root cause**: `effectiveWebRTCVariants` used `append([]models.StreamOutputVariant(nil), variants...)`, which shallow-copied `Outputs` / `Outputs.WebRTC` pointers. `resolveWebRTCLegs` then mutated `webrtcVariants[index].Outputs.WebRTC.Enabled = BoolPtr(false)` in place for disabled copy-fallback legs, mutating `broadcast.OutputSettings` and causing a second `resolveWebRTCLegs` pass (after bitrate measurement) to skip those variants and drop their `disabled` diagnostics.
- **Fix**:
  - Added deep-copy helpers `cloneProtocolOutputSettings` and `cloneOutputVariants` in `services/stream/webrtc.go` and used them in `effectiveWebRTCVariants`.
  - Removed the in-place mutation from `resolveWebRTCLegs`.
  - Applied `disableUnplannedWebRTCVariants` on a fresh clone (`publishedVariants`) only when calling `s.webRTC.SetVariants` and `s.openWebRTCLegs`, after any bitrate re-plan finishes.
- **Files**: `services/stream/webrtc.go`, `services/stream/webrtc_legs.go`, `services/stream/batch2_test.go`.

### #9. HLS sequential output indices mismatched raw config indices across backend and player
- **Root cause**: FFmpeg `-var_stream_map` emits HLS directories sequentially (`0, 1, ...`) for HLS-enabled variants only (preserving golden HLS argv). However, `ThumbnailGenerator`, `applyStreamOffline`, `applyViewerQuality`, `/api/video/variants`, and `sharedVideoVariantFor` used raw `StreamOutputVariants` indices; meanwhile `OwncastPlayer.renditionName()` indexed the bitrate-sorted `/api/video/variants` array by `qualitiesRef.current[variantIndex]` instead of matching `quality.index`.
- **Fix**:
  - Added `hlsEnabledVariants` and `hlsVariantAt` in `services/stream/stats.go` and updated `applyViewerQuality`, `applyStreamOffline`, and `ThumbnailGenerator` to operate strictly on sequential HLS-enabled variant indices.
  - Updated `GetVideoStreamOutputVariants` in `webserver/handlers/video.go` to exclude `!variant.HLSEnabled()` variants and assign sequential `hlsIndex = 0, 1, ...` before bitrate sorting.
  - Added `configIndex` / `hasConfigID` on `transcoder.HLSVariant` so `sharedVideoVariantFor` matches `output.VariantIndex` against the original configured variant index first (falling back to slice index for unit tests constructing `HLSVariant` directly).
  - Updated `OwncastPlayer.tsx` `renditionName()` to find the matching quality via `qualitiesRef.current.find(quality => quality.index === variantIndex)?.name || ''`.
- **Files**: `services/stream/stats.go`, `services/stream/stream.go`, `services/transcoder/transcoder.go`, `services/transcoder/sharedOutput.go`, `webserver/handlers/video.go`, `webserver/handlers/video_test.go`, `web/components/video/OwncastPlayer/OwncastPlayer.tsx`, `web/tests/owncastPlayerFullscreen.test.tsx`, `services/stream/batch2_test.go`.

### #10. RTMP `setCurrentBroadcasterInfo` overwrote probed metadata without `sourceMu`
- **Root cause**: `setCurrentBroadcasterInfo` called `session.SetBroadcaster(&broadcaster)` before `session.setDeclaredDetails(...)`, momentarily overwriting probed `HasBFrames`/Profile/Level without holding `sourceMu`. Furthermore, `publishSourceMetadata`'s dedup `key` omitted `Encoder`, `VideoBitrateSource`, and `VideoOnly`, so a late `onMetaData` matching probed dimensions/codec could be deduplicated away and leave the unmerged snapshot or drop `Encoder`.
- **Fix**:
  - Removed the lockless `session.SetBroadcaster(&broadcaster)` call in `services/rtmp/broadcaster.go`; `setCurrentBroadcasterInfo` now merges via `session.setDeclaredDetails(...)` and forwards the merged `session.Broadcaster()` snapshot to `s.setBroadcaster`.
  - Ensured `setDeclaredDetails` still updates `session.SetBroadcaster` under `sourceMu` when `s.inspector == nil`.
  - Added `Encoder`, `VideoBitrateSource`, and `VideoOnly` to `publishSourceMetadata`'s dedup `key` (and allowed publication when only `Encoder` is populated).
- **Files**: `services/rtmp/broadcaster.go`, `services/rtmp/source_metadata.go`, `services/rtmp/source_metadata_test.go`.

## 2. Verification Results

All checks were executed using workspace-local toolchains (`.toolchains/`):

- **Go (`go1.26.2 linux/amd64`, `CGO_ENABLED=0` because `gcc` is absent in this environment)**:
  - `gofmt -l` on all 19 modified/new Go files: clean.
  - `go vet ./services/webrtc/... ./services/ingest/mpegts/... ./services/rtmp/... ./services/stream/... ./services/transcoder/... ./webserver/handlers/...`: passed (0 issues).
  - `go test -count=1 ./services/ingest/mpegts/...`: passed (including `TestParseSPSWith8x8ScalingList`).
  - `go test -count=1 ./services/rtmp/...`: passed (including `TestRTMPOnMetaDataAfterInspectionPreservesProbedFields`).
  - `go test -count=1 ./services/transcoder/...`: passed (`2.602s`).
  - `go test -count=1 ./services/webrtc/...`: passed (`10.214s`, including `TestServiceVariantsSparseIndices` and `TestEndToEndMediaDelivery`).
  - `go test -count=1 ./services/stream -run "TestAwaitSourceBitrate|TestIngestTee|TestPrefetch|TestEffectiveWebRTCVariants|TestHLSVariantAt|TestSourceInfo|TestWebRTC|TestBroadcast|TestShared|TestIsolated|TestMediaReader|TestFormat"`: passed (all 5 new Batch 2 stream tests plus all existing non-sqlite stream tests).
  - **Not executed due to missing `gcc`/CGO**: `go test -race` and SQLite3-backed `TestMain` execution in `webserver/handlers` (including `TestGetVideoStreamOutputVariantsExcludesHLSDisabledAndRenumbers`, which compiles and passes `go vet` cleanly).
- **Web (`node v24.22.2`, `jest 30.4.2`)**:
  - `npm run typecheck` (`tsc --noEmit`): passed (0 errors).
  - `npx eslint components/video/OwncastPlayer/OwncastPlayer.tsx tests/owncastPlayerFullscreen.test.tsx`: passed (0 errors).
  - `npx prettier --check components/video/OwncastPlayer/OwncastPlayer.tsx tests/owncastPlayerFullscreen.test.tsx`: passed.
  - `jest` on `owncastPlayerFullscreen`, `playerContainer`, `playerControls`, `videojsDispose`: **4/4 suites passed, 33/33 tests passed**.

## 3. Remaining Audit Batches (Not Yet Applied)

- **Batch 3 (Stats panel & admin observability)**: `#11` (VHS `vhs.stats.*` & Auto representation), `#13` (WebRTC `estimatedPlayoutTimestamp` NTP epoch & selected ICE RTT), `#15` (HLS `clockSkew`), `#20` (admin status & FFmpeg panel accuracy), `#21`-`#25` (WebRTC sparkline zero spike, StatsPanel fields/UX, WebRTC `.bigUnmuteButton` positioning, `LatencyCompensator` manual quality lock, `PlaybackMetrics` global XHR cleanup & embed `streamIdentity`).
- **Batch 4 (Robustness & edge cases)**: `#16`-`#19`, `#26`-`#27`.
