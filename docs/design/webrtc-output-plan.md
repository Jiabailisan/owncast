# WebRTC output plan: passthrough, fallback and shared encodes

Status: implemented on `feat/srt-webrtc-develop`.

This note describes how Owncast decides what each WebRTC rendition is, why the
"original quality" tier used to fall back silently, and the rules the
implementation now enforces.

## 1. The decision pipeline

For every output variant that is exposed over WebRTC:

1. **Requested mode** — the variant's `outputs.webrtc.mode`: `inherit`,
   `transcode` or `copy`.
2. **Resolved mode** — `inherit` ("Follow HLS settings") resolves against the
   HLS variant: an HLS copy (video passthrough) variant inherits `copy`,
   anything else inherits `transcode`.
3. **Copy eligibility** — a copy leg is only allowed when the source is
   browser-safe H.264: H.264 codec, a browser-safe profile, B-frames known and
   absent, resolution known, even and at most 4096x2160, and an H.264 level at
   or below 5.1 (or the configured level when that is lower).
4. **Fallback** — when the copy cannot be used, `copyFallback` decides:
   `transcode` (default) keeps the rendition and transcodes it **at the source
   size and frame rate**; `disable` withdraws only this WebRTC rendition.
5. **Encoder plan** — target and peak bitrate, GOP, frame rate, scale and the
   output profile.

Every step is reported: `GET /api/admin/live/status` carries
`webrtc.legs[]` with the requested mode, what an inheritance resolved against,
the applied mode, the source properties the rules used, the encoder plan, the
measured output bitrate, and every failed copy rule. The same decision is
logged once per leg at stream start.

## 2. Why the original-quality tier fell back silently

The SRT copy decision waits for the MPEG-TS inspector's B-frame verdict, and the
read-ahead that feeds the inspector was bounded by a fixed **1 MiB**. At 13 Mbps
(2560x1440@60) one megabyte is about 0.6 s of media, while the verdict needs
about a second of video, so the budget was exhausted first. The wait then
returned with "source metadata is not available yet", the inherited copy fell
back to transcode, and — because an inherited fallback produced no warning —
nothing in the admin said so. The replacement encode then reused the 1080p HLS
rendition's encode through the shared tee, so the tier the operator configured
as passthrough was delivered as the 1080p rendition at that rendition's 5 s GOP.

Fixes:

- The read-ahead is bounded by **time** (the same 3.5 s window as the wait) with
  a 32 MiB memory ceiling, so a high-bitrate source can no longer lose its
  verdict. The wait also stops early when the codec is known to be non-H.264.
- An inherited fallback is reported with its own code
  (`webrtc_inherited_copy_fallback`) and its reasons.

## 3. Transcode-at-source-size

A passthrough variant has no configured dimensions or frame rate, so a fallback
transcode keeps the source resolution and frame rate instead of collapsing to
the 2500 kbps / 30 fps defaults:

- scale: empty (no scaling) unless the variant or its WebRTC override sets one;
- frame rate: the measured source rate, unless configured;
- bitrate: an explicit per-protocol override, then the variant's own bitrate for
  a transcoding variant, then the **measured** source video bitrate, then a
  passthrough variant's declared bitrate, then the documented default. The
  choice is recorded (`explicit`, `measured`, `declared`, `default`) and shown.
- peak: an explicit `outputs.webrtc.peakBitrateKbps`, otherwise the documented
  window above the target. The window is fixed when the stream starts; a source
  bitrate that moves later never restarts the encoder.
- The automatic target waits at most 2.5 s for the measured source rate before
  falling back to the default, and only when a source-size transcode leg would
  otherwise use the default.
- The admin field is "Video Bitrate" and the operator fills in the video rate
  they want, so a WebRTC transcode leg targets exactly that value; no audio room
  is deducted from it. (The HLS encoder still deducts its historical 192 kbps
  audio room from the same field — that behavior is frozen by the golden argv —
  which is an upstream semantic mismatch worth fixing separately.)
- Consequence for sharing: the shared HLS encode is pinned to
  `videoBitrate - 192` (upstream's historical rule, frozen by the v0.3.1 golden
  argv — the same field is used there and the fixture captured a live 0.3.1
  binary), so a transcode leg targeting `videoBitrate` can never match it and
  always gets its own encode. Sharing still happens when the leg's target equals
  the HLS encoder's effective rate, which an operator can arrange with a
  per-protocol `videoBitrate` override of `videoBitrate - 192`. This costs a
  second encode per transcode tier in the default configuration; the alternative
  — changing the HLS rate too — would break the golden argv gate and silently
  change every existing configuration's output rate, so it is left to upstream.

## 4. Shared encode reuse

The shared process model may tee one encode to HLS and WebRTC **only when the
HLS encode already satisfies the leg's whole output plan**:

| parameter | rule |
|---|---|
| passthrough | a copy leg may only reuse a passthrough variant, a transcode leg only an encoded one |
| resolution | the rendered scale expression must be identical |
| frame rate | identical |
| rate control | the leg's target must equal the variant's allocated video bitrate |
| GOP | the leg's keyframe interval must equal the variant's segment-aligned GOP |
| profile | the shared encoder writes the configured WebRTC profile |
| B-frames | the shared encoder writes `bframes=0`, which every transcode leg asks for |

A leg whose plan does not match any HLS encode gets its **own encode in the same
FFmpeg process**; it is never fed a rendition that does not match its plan. Only
the variants a leg actually reuses get the WebRTC-compatible encoder
constraints, so the shared mode cannot leak into an unrelated HLS rendition.

Because the GOP rule compares the leg's keyframe interval with the HLS
segment-aligned GOP, sharing requires `keyframeIntervalSeconds` to equal the
latency level's segment duration. With the defaults (1 s vs 3 s) the encode is
not shared, which is the honest answer: a 3 s GOP cannot serve a 1 s
keyframe-interval leg. Aligning the two restores sharing.

## 5. Unified RTMP and SRT detection

Both protocols now inspect the actual bitstream and share one implementation of
the H.264 rules (`mpegts.H264Probe`):

- SRT: the MPEG-TS inspector (SPS, slice headers, PES bitrate).
- RTMP: a new FLV/AVC inspector fed from the packet pump (`av.Packet`), which
  reads the AVCDecoderConfigurationRecord, the AVCC NAL units, the AAC
  AudioSpecificConfig and a media-time video bitrate window.

The RTMP connect callback now runs beside the packet pump instead of before it:
the pump is the only source of media bytes, so a synchronous callback that waits
for a verdict could never receive one.

`onMetaData` is still used, but only where the bitstream has no answer (the
encoder name) or as a fallback before a measurement exists. This removes the
"unverified copy" path: an RTMP passthrough leg is now subject to the same rules
as an SRT one.

## 6. Late-joining processes and the FLV container

The isolated process model starts the WebRTC process beside the HLS one, and
restarts it on failure, so its ingest branch begins in the middle of the
container. MPEG-TS resynchronizes on its 188-byte sync bytes; FLV does not, and
worse, FLV carries the decoder configuration in a sequence-header tag that is
sent exactly once at the start of the stream. A late branch therefore had no
file header and no extradata, and the process failed with "Invalid data found"
(and, before that, silently delivered nothing).

The tee now gives such a branch a container prefix plus tag alignment:

- the session exposes `ContainerPrefix()`, which for RTMP rebuilds the FLV file
  header and the cached video and audio sequence-header tags;
- `flvAligner` buffers the first chunk until it can confirm two consecutive
  valid FLV tag headers, then writes the prefix followed by that tag.

This makes RTMP work under the isolated process model at all: before the fix the
WebRTC process could never decode an FLV source, on the first attempt or on a
restart.

## 6a. Late-joining branches in the isolated process model (MPEG-TS)

The isolated model fans the ingest stream out to two FFmpeg processes through a
tee. The WebRTC branch was registered *after* the pump started, so the stream
head — PAT/PMT and the H.264 parameter sets, which MPEG-TS sends with the first
keyframe — reached only the HLS branch. A stdin capture proved it: the WebRTC
side began with 40 bytes of a torn packet, saw its first PAT 50 KB in, and
never received an SPS/PPS/IDR; its FFmpeg probe failed with
`non-existing PPS 0 referenced` and `unspecified size`, exactly what the
field reported.

Two changes fix the join:

- the tee's pump is gated on `Start()`, and the first WebRTC branch is
  registered before it, so both branches see the stream from byte 0;
- a late branch (every supervised restart) passes its head through a container
  aligner: MPEG-TS drops the partial packet, FLV keeps the rebuilt-header rule.

Post-fix captures show both branches byte-identical (15.9 MB each) with PAT at
188 B and SPS/PPS/IDR at 597/626/1274 B, and the probe errors are gone.

Two shutdown windows in the same area are also closed. The pump's EOF path runs
`finishAll` before the deferred `close(stopped)`, and a branch registered in
that gap used to receive a live pipe nobody writes to or closes — the same
hang on the Close path. The finished flag is now set in the same critical
section that snapshots the branches, and `addBranch` checks it under that lock:
a late branch either is finished by the pump or gets an already-closed pipe.
A targeted test hammers registrations during the shutdown window (200 attempts)
and asserts every branch sees EOF; it fails against the pre-fix code.

## 7. Verification

A 2560x1440@60, Main/5.1, no-B-frame H.264 source was published over SRT and
over RTMP, in both process models, with the original-quality tier set to
`Follow HLS settings` and to explicit `Copy`. The field's exact input profile
(High@5.1, 2560x1440@60) was then re-run through the two hardest cells (SRT /
shared / inherit and RTMP / isolated / inherit) with the same result. A real WHEP viewer then read the
received H.264 RTP and reported the resolution from the SPS
(`test/automated/webrtc/whep_resolution_e2e_test.go`), because the SDP answer
only advertises a profile-level-id:

| cell | tier 0 | tier 1 |
|---|---|---|
| SRT / shared / inherit | copy, 2560x1440 | transcode, 1920x1080 |
| SRT / shared / copy | copy, 2560x1440 | transcode, 1920x1080 |
| SRT / isolated / inherit | copy, 2560x1440 | transcode, 1920x1080 |
| SRT / isolated / copy | copy, 2560x1440 | transcode, 1920x1080 |
| RTMP / shared / inherit | copy, 2560x1440 | transcode, 1920x1080 |
| RTMP / shared / copy | copy, 2560x1440 | transcode, 1920x1080 |
| RTMP / isolated / inherit | copy, 2560x1440 | transcode, 1920x1080 |
| RTMP / isolated / copy | copy, 2560x1440 | transcode, 1920x1080 |

Diagnostics and policies:

- a source with B-frames reports `webrtc_copy_ineligible_b_frames` and is
  transcoded at the source size (1280x720 for the 720p fixture);
- a source whose B-frame verdict never arrives reports
  `webrtc_copy_ineligible_b_frames_unknown` and falls back the same way;
- `copyFallback=disable` withdraws only the target tier: WHEP answers 400
  `webrtc_variant_not_found` for it while the other tier keeps delivering;
- the field's shape (HLS 5 s GOP = 300 frames at 60 fps, WebRTC 1 s GOP = 60)
  does not reuse the HLS encode: the leg gets its own encode with `-g 60` in the
  same process;
- an aligned keyframe interval (3 s segments, 3 s interval) does share: one
  encode, two tee slaves;
- explicit VBR (`videoBitrate=3000`, `peakBitrateKbps=5000`) produces
  `-b:v 3000k -minrate 3000k -maxrate 5000k -bufsize 5000k`;
- a measured source rate is used as the automatic target (2553 kbps for a
  measured 2474 kbps source);
- a source too slow for the bitrate window reports
  `webrtc_transcode_bitrate_default` and uses the documented 2500 kbps default.

## 8. Behavior changes for existing configurations

- An inherited copy that cannot be used now reports a warning and its reasons
  instead of falling back silently.
- A fallback transcode keeps the source size and frame rate, and targets the
  measured source bitrate when it is available (previously 2500 kbps / 30 fps).
- A WebRTC transcode leg for a transcoding variant targets the variant's video
  bitrate (its configured bitrate minus the 192 kbps audio room) instead of the
  muxed total, so the HLS and WebRTC outputs of one variant describe one
  rendition.
- WebRTC transcode legs use a narrow VBR window (target .. target + half the
  target, capped at 2000 kbps) instead of a pinned CBR target.
- The shared tee no longer reuses an HLS encode whose output plan does not
  match the leg; such a leg is encoded separately in the same process.
- RTMP copy legs are no longer delivered "unverified"; they must pass the same
  eligibility rules.
- A video-only source no longer produces Opus outputs or `a:N` stream-map
  entries, which used to fail the whole FFmpeg process.