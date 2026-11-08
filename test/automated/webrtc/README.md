# WebRTC / WHEP acceptance harness (T3/T4)

Pion-based WHEP test client for a **running** Owncast. The Go test does not start
or stop the server: QA owns the managed instance (isolated ports/netns).

## Run

```bash
cd owncast
go test -tags=integration -count=1 -timeout 5m ./test/automated/webrtc/...
```

Without a reachable server every test **skips** with a clear reason, so the
command is safe to run on a developer machine.

## Environment contract

| Variable | Default | Meaning |
| --- | --- | --- |
| `OWNCAST_BASE_URL` | `http://127.0.0.1:18081` | Managed server base URL |
| `OWNCAST_WHEP_VARIANT` | `0` | WebRTC variant index used in `?variant=` |
| `OWNCAST_WHEP_EXPECT_LIVE` | unset | `1` = a live publisher exists; enables `TestWHEPMediaDelivery` (T3/T4) |
| `OWNCAST_EXPECT_INPUT_PROTOCOL` | unset | `rtmp` or `srt`; asserts `/api/status.inputProtocol` when media test runs |
| `OWNCAST_WHEP_EXPECT_LINK` | unset | `1` = stunServers/turnServers configured, require `Link: rel="ice-server"` |
| `OWNCAST_WHEP_CAPACITY_TEST` | unset | `1` = server runs with `maxViewers=1`; enables 503 capacity test |
| `OWNCAST_WHEP_RATELIMIT_TEST` | unset | `1` = server runs with `signalingRateLimit=1`; enables 429 test |
| `OWNCAST_WHEP_STRICT_DOMAIN_TEST` | unset | `1` = server runs with `strictDomain=true` |
| `OWNCAST_WHEP_DOMAIN` | unset | Required with the strict-domain test: the configured domain |

The harness reads `/api/config` first and skips when `webrtc.enabled` is not
true, so a misconfigured stage reports SKIP instead of a false failure.

## What is asserted

`TestWHEPContract` (no media required):
- `OPTIONS /webrtc/whep` -> 204 + `Accept-Post: application/sdp`
- `GET /webrtc/whep` -> 405
- wrong content type -> 415
- malformed SDP -> 400 `webrtc_bad_offer`
- VP8-only offer -> 422 `webrtc_unsupported_media`
- unknown variant -> 400/404
- real offer -> 201 with `Location: /webrtc/whep/session/<id>`, ETag, SDP answer with video+audio m-lines, optional `Link rel="ice-server"`
- trickle PATCH -> 204
- DELETE -> 204 and idempotent

`TestWHEPMediaDelivery` (T3/T4, requires `OWNCAST_WHEP_EXPECT_LIVE=1`):
- asserts `/api/status.online` and, when set, `inputProtocol`
- Pion viewer with `DisableSRTPReplayProtection(true)`, receiver RTCP drained
- non-trickle offer -> 201 -> answer -> ICE connected (DTLS-SRTP)
- inbound H.264 video RTP and Opus audio RTP (codec asserted)
- sends a PLI over RTCP to exercise the feedback path (server cannot force an
  FFmpeg keyframe, so recovery is not asserted; per-viewer counters are covered
  by services/webrtc integration tests)
- DELETE closes the viewer connection

`TestWHEPCapacityRejection` / `TestWHEPRateLimit` / `TestWHEPStrictDomain`:
env-gated contract tests for 503+`Retry-After`, 429+`Retry-After` and 403
`webrtc_domain_mismatch`.

## Live-stream prerequisite (QA wiring suggestion)

The harness only needs `/api/status.online == true` and a WebRTC-enabled output
variant. A minimal publisher for the stage:

```bash
ffmpeg -re -i test-data/sample-720p30-nob.flv -c copy -f flv rtmp://127.0.0.1:11936/live/<streamkey>
# or SRT (see tools/scripts/ for the managed-server helpers):
ffmpeg -re -i test-data/sample-720p30-nob.flv -c copy -f mpegts "srt://127.0.0.1:18890?streamid=publish:/live/<streamkey>"
```

Set `OWNCAST_EXPECT_INPUT_PROTOCOL=rtmp` (or `srt`) per stage so a mismatch
between the publisher and the asserted protocol fails loudly.
