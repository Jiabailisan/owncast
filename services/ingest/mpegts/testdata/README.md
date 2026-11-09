# MPEG-TS inspector test fixtures

`sample-720p30-nob-2s.ts` is a 2 second stream-copy cut of the workspace QA
source `test-data/sample-720p30-nob.ts` (H.264 Main 1280x720 30fps no
B-frames + AAC-LC 48 kHz stereo, encoded at -b:v 2500k -maxrate 3000k
-b:a 128k). It is committed so the inspector's bitrate and AAC detail tests run
hermetically in CI without FFmpeg or the workspace test-data directory.

Provenance (2026-11-09):

| Field | Value |
|---|---|
| source | test-data/sample-720p30-nob.ts (sha256 b35089d46b6d3c50...) |
| command | ffmpeg -ss 0 -i <source> -t 2 -c copy -f mpegts sample-720p30-nob-2s.ts |
| sha256 | c1587a6d490c5a9c8565446e2806050bb2b7edb1d6bb0255ed3c137f5fb9a707 |
| ffprobe | h264 Main 1280x720; aac LC 48000 Hz 2 ch ~130 kbps |
