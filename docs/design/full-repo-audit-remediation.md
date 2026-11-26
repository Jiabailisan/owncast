# Full-Repository Audit Remediation (P0 / P1 / P2 + Hygiene)

## Summary

This document records the phased remediation of all 49 findings identified in the full-repository audit across the Go media pipeline, backend security/lifecycle/persistence layer, Next.js frontend/player, Service Worker, and CI workflows on branch `feat/srt-webrtc-develop`.

## Phase 1: P0 Remediation (16 Items)

- **Media pipeline (`a18265b`)**:
  - **M-P0-1**: Derived WebRTC janitor `runCtx` from `context.Background()` so bounded restart startup contexts no longer cancel the janitor goroutine.
  - **M-P0-2**: Enforced `:format=nv12` on `scale_cuda`, added `SetSourceCodec` normalization (`H.264`/`H.265`/profiles) and SW-scale fallback for non-H.264/HEVC or 10-bit/4:2:2/4:4:4 profiles across shared and isolated FFmpeg processes, added `scale_cuda=format=nv12,hwdownload,format=nv12` for cross-encoder CUDA->CPU RTP legs, and bounded `ffmpegSupportsFilter` with a 5s timeout.
  - **M-P0-3**: Synchronized `S3Storage.queuedPlaylistUpdates`, moved `Save` outside `s.lock`, and grouped `getDeletableVideoSegmentsWithOffset` per variant directory while protecting `offline-v2.ts`.
  - **M-P0-4**: Handled WebRTC-only configurations (zero HLS variants) as RTP-only across transcoder flags, directory setup, isolated mode, and offline transitions.
  - **M-P0-5**: Guarded `s.stats` snapshots, `offlineTranscoder`, `thumbnailGen`, `onlineTimerCancelFunc`, `s.storage` swaps, and `StartOfflineCleanupTimer` epoch serialization.
- **Backend security & stability (`15e0ac4`)**:
  - **B-P0-1**: Scoped `owncast_admin_session` cookie strictly to `Path: "/plugins/"`, restricted cookie auth to a `/plugins/` path allowlist, bounded `adminSessionStore` to 256 entries, primed the cookie only on admin entry/plugin routes, rejected active web asset uploads in `pluginhost`, and added `X-Content-Type-Options: nosniff` + `Content-Security-Policy: sandbox` (plus no directory listing) on `/public/*`.
  - **B-P0-2**: Enforced exact canonical actor IRI binding (scheme, host, canonical port, path, and query) in ActivityPub inbox verification.
  - **B-P0-3**: Fixed bot scraper HTML cache to a single bounded cache container keyed only when canonical `ServerURL` is configured.
  - **B-P0-4**: Bounded CMCD batches (`16`), playback metrics payload size (`16 KiB`), `clientPlayback`, and `selfReportingClients` (`10,000` cap with nil-state guards).
  - **B-P0-5**: Guarded `Loaded.callPlugin` against nil `l`/`l.plugin`.
  - **B-P0-6**: Guarded `SqlChatMessageRepository.historyCache` with `sync.RWMutex` and capped `GetChatModerationHistory` to 1,000 rows.
  - **B-P0-7**: Removed `log.Fatalln` crash paths from `GetServerConfig` and `utils.RenderPageContentMarkdown`.
- **Frontend, SW & CI (`d07bd42`)**:
  - **F-P0-1**: Derived viewer-only `_next/static` precache entries in `web/build-scripts/generate-sw.js` from non-admin exported HTML/CSS, dropped legacy `.woff` and duplicate static Inter fonts, and added an 8 MB ceiling (shrinking precache from 22.06 MB / 679 files to ~5 MB / ~113 files with 0 admin leaks).
  - **F-P0-2**: Scoped `PlayerControls` hover-pinning to mouse pointers inside `.controlBar` only, fixing fullscreen/desktop/touch autohide and touch tap-to-toggle.
  - **F-P0-3**: Saved bound listener refs, detached all 8 listeners and cleared all timers in `LatencyCompensator.disable()`, guarded `handleBuffering()` with `if (!this.enabled) return;`, and reused one instance per player.
  - **F-P0-4**: Reset `turnServerList` to typed array default `[]` instead of `""`, cleared `SettingField` local drafts via `resetNonce` on Save/Cancel, enabled Reset whenever value differs from default, and removed the `passphraseSet` fallback.
  - **F-P0-5**: Fixed inverted Prettier `--check`/`--write` steps in `javascript-format-test-build.yml`, added `typecheck` to `web-bundle.needs`, and standardized `npm ci`.

## Phase 2: P1 Remediation (21 Items)

- **Media pipeline (`37141d2`)**: Checked `VideoCodec` in `variantMatchesLeg`; switched `sliceTypeOf` to a zero-allocation `[16]byte` stack buffer with post-verdict short-circuiting on both SRT and RTMP; added MPEG-TS `0x06` Opus descriptor detection and gated non-H.264/non-AAC ES scanning; added bounded stdout/stderr pipe drain before `cmd.Wait()`; made `.m3u8` writes atomic via temp+rename; propagated refreshed source bitrate back to `SetSourceDetails` and omitted `,mp4a.40.2` on video-only streams; hardened `reorderBuffer` against late packets and producer restarts; preferred registered 4-char H.264 profile prefixes in SDP selection; reset SRT bitrate tracker on large backward PTS jumps; skipped redundant GeoIP goroutines in `SetViewerActiveForSnapshot`; implemented `io.Closer` on `prefetchReader` and RTMP `countingReader`.
- **Backend (`e9dccd0`)**: Added SQLite `_busy_timeout=5000&_txlock=immediate`, `0600` DB creation, `Datastore.Close()`, synchronized `WarmCache`/`Reset`, two-stage streaming SQLite backup releasing the 1-conn pool before gzip, and 500-ID batching in `SetMessageVisibilityForMessageIDs`; introduced fresh 10s shutdown contexts in `main.go` and `metrics.Service.Stop()`; fixed inverted `ChatSpamProtectionDisabled`, `RunningProcessModel()` pending comparison, `SetAdminPasswordChecked` empty-password rejection and session invalidation on success, `stripSetCookie` hardening + `owncast_chat_token` stripping, 50 MiB per-plugin upload quota, ActivityPub unparseable `Date` rejection + pre-fetch case-insensitive domain blocklist, `RemoteFollow` URL scheme validation + body limit, Discord 10s timeout + HTTP status validation, chat `Client.GetUser/SetUser/GetGeo/SetGeo` and `writePump` race fixes, and `metrics` initialization/mutex/slice-cap/getter fixes.
- **Frontend (`b83730e`)**: Switched HLS `LatencyCompensator` to `qualityPinMode: 'current'` with manual-selection snapshot clearing; fixed WebRTC `&:fullscreen` `object-fit: contain` and Safari container fullscreen precedence; updated `OutboundStreamsTable` and `config/settings/webrtc.go` copy-vs-transcode hints; avoided zero-bitrate audio sparkline pollution when audio is absent; fixed `Space`/`0`/`9` hotkeys, WebSocket `PING` framing + dev/custom port handling + timer cleanup, `ClientConfigStore` `chatDisabled` dependency + history deduplication, and `_app.tsx` SW registration `.catch`.

## Phase 3: P2 Polish & Hygiene (`8eb0122` + Phase 4)

- Paused admin live status polling when switching away from the Status tab and fixed `useLiveStatusPolling` abort/backoff edge cases.
- Kept HLS Stats for nerds sparklines across quality switches, added Safari `webkitPresentationMode` PiP support, and preserved volume on mute.
- Made `useFederatedServers.test.tsx` timezone-independent, replaced vacuous assertion in `playerControls.test.tsx`, excluded `!styles/antd.css` in `css-lint.yaml`, and upgraded `static-web-verify.yml` to detect untracked files.
- Cleaned up `offline-v2.ts*` temp files, added 30s `CommandContext` timeout + double-Start guard to `ThumbnailGenerator`, ignored `ENOENT` in `localCleanup`, verified file existence before viewer counting in `hls.go` and before 304 responses in `web.go`, and updated `WarningCopyProfileMismatch`.
- Removed deprecated `WebRTCOutputTable.tsx` + story, removed 5 unused helper exports and 23 redundant default exports, deduplicated autoplay preference reading in `OwncastPlayer.tsx` so `npx knip --include dependencies,files,exports` exits `0`, and rebuilt `static/web/` with reproducible verification.
