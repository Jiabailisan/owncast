# SRT integration tests (test/automated/srt)

Automated SRT ingest coverage for spec section 12 (T2, T10, T11). The real work
is driven by the canonical QA harness so there is a single implementation:

    tools/scripts/run-srt-e2e.sh

This directory is the conventional upstream test location; run.sh delegates to
the harness and forwards flags.

## Current status (2026-11-09)

SRT ingest is wired (services/srt) and the admin config API exists. The harness
enables SRT through the supported operator path (GET /api/admin/config/srt ->
PUT enabled=true/bindAddress/port -> restart-service restart -> verify listener
via config API + UDP socket ownership) and then runs the media/auth matrix:
MPEG-TS publish -> /api/status online+inputProtocol=srt -> HLS master/variant/
segments 200 -> transcoder argv `-f mpegts -i pipe:0` -> offline transition ->
session-end stats; plus bad key 1002, malformed streamid, 3 streamid formats,
second SRT publisher 1003, RTMP-while-SRT rejected, passphrase 1011 + CIDR deny.

Isolation: default ports 18081/11936, state dir tagged with the port pair;
`QA_NETNS=1` runs the whole suite in an unprivileged network namespace so
concurrent agents cannot collide (including SRT UDP 8890).

Latest evidence: docs/testing/test-results.md (canonical gate #1: 26 pass /
0 fail / 3 pre-enablement BLOCKED).
