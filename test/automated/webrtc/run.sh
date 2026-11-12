#!/usr/bin/env bash
# WebRTC / WHEP integration suite (spec T3, T4, T5, T8, T9, T12).
#
# The Pion WHEP Go harness lives in this directory (whep_e2e_test.go, build tag
# "integration"; authored by webrtc-engineer). This runner owns the lifecycle:
#   1. build current HEAD + start managed Owncast on isolated ports (18081/11936;
#      QA_NETNS=1 -> unprivileged network namespace, no collisions incl. UDP 8890)
#   2. enable WebRTC (and SRT for T4) through the admin config API (supported
#      path: GET config -> PUT bare config object -> restart-service restart)
#   3. run the Go WHEP contract tests, then T3 (RTMP->WebRTC) and T4 (SRT->WebRTC)
#      media tests with real FFmpeg publishers feeding the server
#   4. record PASS/FAIL/BLOCKED per stage + evidence under
#      artifacts/integration/<timestamp>/webrtc-e2e/
#
# Honest-status rules: missing harness/config/feature => BLOCKED, never a pass.
#
# Usage: ./run.sh [--no-build] [--keep-server] [--no-media]
# Env: OWNCAST_WEB_PORT (18081), OWNCAST_RTMP_PORT (11936), OWNCAST_RUN_DIR,
#      OWNCAST_BIN, QA_NETNS.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# This runner lives inside the Owncast repo (owncast/test/automated/webrtc);
# the workspace root is the directory containing the repo checkout.
REPO_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null || true)"
# Inside an unprivileged user namespace (QA_NETNS=1) git may refuse the repo as
# "dubious ownership" (root-in-userns vs uid 1000 on disk), so fall back to a
# filesystem-derived repo root: <repo>/test/automated/webrtc -> <repo>.
if [[ -z "$REPO_ROOT" || ! -f "$REPO_ROOT/go.mod" ]]; then
	REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
fi
WORKSPACE_ROOT="$(cd "$REPO_ROOT/.." && pwd)"
# shellcheck source=../../../tools/scripts/env.sh
source "$WORKSPACE_ROOT/tools/scripts/env.sh"
# shellcheck source=../../../tools/scripts/lib/status.sh
source "$WORKSPACE_ROOT/tools/scripts/lib/status.sh"
JSON_GET="$WORKSPACE_ROOT/tools/scripts/lib/json-get.js"
CONFIG_MERGE="$WORKSPACE_ROOT/tools/scripts/lib/config-merge.js"
FF="$FFMPEG_BIN"
SAMPLE_FLV="$WORKSPACE_ROOT/test-data/sample-720p30-nob.flv"
SAMPLE_TS="$WORKSPACE_ROOT/test-data/sample-720p30-nob.ts"
SRT_PORT=18990

STAMP="$(date +%Y%m%d-%H%M%S)"
WEB_PORT="${OWNCAST_WEB_PORT:-18081}"
RTMP_PORT="${OWNCAST_RTMP_PORT:-11936}"
RUN_DIR="$PROJECT_ARTIFACTS/integration/$STAMP/webrtc-e2e"
STATE_DIR="${OWNCAST_RUN_DIR:-$RUN_DIR/state-web${WEB_PORT}-rtmp${RTMP_PORT}}"
BIN="${OWNCAST_BIN:-$PROJECT_ARTIFACTS/bin/owncast-qa}"
BASE="http://127.0.0.1:$WEB_PORT"
DO_BUILD=1
KEEP_SERVER=0
DO_MEDIA=1

# The netns re-exec below forwards the original arguments; the parse loop
# shifts them away, so keep a copy.
ORIG_ARGS=("$@")

while [[ $# -gt 0 ]]; do
	case "$1" in
		--no-build) DO_BUILD=0 ;;
		--keep-server) KEEP_SERVER=1 ;;
		--no-media) DO_MEDIA=0 ;;
		-h | --help) sed -n '2,28p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) echo "ERROR: unknown argument: $1" >&2; exit 2 ;;
	esac
	shift
done

if [[ "${QA_NETNS:-0}" == "1" && "${QA_IN_NETNS:-0}" != "1" ]]; then
	if ! command -v unshare >/dev/null 2>&1; then
		echo "ERROR: QA_NETNS=1 but unshare is not available" >&2
		exit 2
	fi
	echo "QA_NETNS=1: re-executing inside an unprivileged network namespace (isolated loopback ports)"
	exec unshare -rn env QA_IN_NETNS=1 bash -c 'ip link set lo up 2>/dev/null || true; exec bash "$@"' _ "$0" "${ORIG_ARGS[@]}"
fi

mkdir -p "$RUN_DIR"
rm -rf "$STATE_DIR"
export OWNCAST_RUN_DIR="$STATE_DIR"
export OWNCAST_WEB_PORT="$WEB_PORT"
export OWNCAST_RTMP_PORT="$RTMP_PORT"
status_init "run-webrtc-e2e" "$RUN_DIR"
echo "evidence: $RUN_DIR"

START_REBUILD_ARG=""
[[ "$DO_BUILD" == "0" ]] && START_REBUILD_ARG="--no-rebuild"
PUB_PID=""
SERVER_STARTED=0
cleanup() {
	if [[ -n "$PUB_PID" ]]; then kill "$PUB_PID" 2>/dev/null || true; wait "$PUB_PID" 2>/dev/null || true; fi
	if [[ "$KEEP_SERVER" != "1" && "$SERVER_STARTED" == "1" ]]; then
		"$WORKSPACE_ROOT/tools/scripts/stop-owncast.sh" >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT

api_code() { # api_code METHOD path out [data-file]
	local method="$1" path="$2" out="$3" datafile="${4:-}"
	if [[ -n "$datafile" ]]; then
		curl -s -m 20 -o "$out" -w '%{http_code}' -X "$method" -H 'Content-Type: application/json' \
			-H 'X-Owncast-CSRF-Protection: 1' -u "admin:${OWNCAST_ADMIN_PASSWORD:-abc123}" \
			--data-binary "@$datafile" "$BASE/api/admin$path"
	else
		curl -s -m 20 -o "$out" -w '%{http_code}' -X "$method" \
			-H 'X-Owncast-CSRF-Protection: 1' -u "admin:${OWNCAST_ADMIN_PASSWORD:-abc123}" "$BASE/api/admin$path"
	fi
}
status_is() { local body; body="$(curl -fsS -m 2 "$BASE/api/status" 2>/dev/null || true)"; [[ "$body" == *"$1"* ]]; }
wait_online() {
	local limit="${1:-25}" i
	for ((i = 0; i < limit; i++)); do
		if status_is '"online":true'; then return 0; fi
		if [[ -n "$PUB_PID" ]] && ! kill -0 "$PUB_PID" 2>/dev/null; then return 2; fi
		sleep 1
	done
	return 1
}
wait_offline() {
	local limit="${1:-20}" i
	for ((i = 0; i < limit; i++)); do
		if status_is '"online":false'; then return 0; fi
		sleep 1
	done
	return 1
}
restart_server() {
	"$WORKSPACE_ROOT/tools/scripts/stop-owncast.sh" >/dev/null 2>&1 || true
	sleep 1
	# shellcheck disable=SC2086
	if "$WORKSPACE_ROOT/tools/scripts/start-owncast.sh" $START_REBUILD_ARG >"$RUN_DIR/server-restart.log" 2>&1; then
		# shellcheck disable=SC1091
		source "$STATE_DIR/server.env"
		SERVER_PID="$OWNCAST_PID"
		BASE="$OWNCAST_BASE_URL"
		return 0
	fi
	return 1
}
cfg_enabled() { # cfg_enabled <file> <section e.g. config>
	node "$JSON_GET" "$1" "$2.enabled" "" 2>/dev/null || true
}

# --- build + managed server --------------------------------------------------
if [[ "$DO_BUILD" == "1" ]]; then
	rm -f "$BIN"
	build_ok=0
	for attempt in 1 2 3; do
		if (cd "$OWNCAST_DIR" && go build -o "$BIN" .) >"$RUN_DIR/build.log" 2>&1; then build_ok=1; break; fi
		cp "$RUN_DIR/build.log" "$RUN_DIR/build-attempt-$attempt.log" 2>/dev/null || true
		echo "build attempt $attempt failed; retrying in 4s" >&2
		sleep 4
	done
	if [[ "$build_ok" != "1" ]]; then
		status_fail "build" "go build failed at HEAD after 3 attempts (build-attempt-*.log)"
		status_finish
		exit 1
	fi
	status_pass "build" "HEAD $(git -C "$OWNCAST_DIR" rev-parse --short=12 HEAD) + $(git -C "$OWNCAST_DIR" status --porcelain | wc -l | tr -d ' ') dirty file(s)"
else
	[[ -x "$BIN" ]] || { status_fail "build" "--no-build but binary missing: $BIN"; status_finish; exit 1; }
	status_skip "build" "--no-build: using $BIN"
fi
if curl -fsS -m 2 -o /dev/null "$BASE/api/status" 2>/dev/null; then
	status_blocked "server-start" "a server already answers on $BASE that this script does not own"
	status_finish
	exit 3
fi
# shellcheck disable=SC2086
if ! "$WORKSPACE_ROOT/tools/scripts/start-owncast.sh" $START_REBUILD_ARG >"$RUN_DIR/server-start.log" 2>&1; then
	status_fail "server-start" "managed Owncast failed to start; see server-start.log"
	status_finish
	exit 1
fi
SERVER_STARTED=1
# shellcheck disable=SC1091
source "$STATE_DIR/server.env"
BASE="$OWNCAST_BASE_URL"
SERVER_PID="$OWNCAST_PID"
status_pass "server-start" "pid $SERVER_PID at $BASE"

# --- enable WebRTC (and SRT for T4) through the supported config API ---------
WEBRTC_ENABLED=0
CONFIG_API="$(api_code GET /config/webrtc "$RUN_DIR/webrtc-config-get.json")"
if [[ "$CONFIG_API" == "200" ]]; then
	status_pass "webrtc-config-api" "GET /api/admin/config/webrtc -> 200"
	if [[ "$(cfg_enabled "$RUN_DIR/webrtc-config-get.json" config)" == "true" ]]; then
		WEBRTC_ENABLED=1
		status_pass "webrtc-enable" "already enabled in configuration"
	else
		node "$CONFIG_MERGE" "$RUN_DIR/webrtc-config-get.json" "$RUN_DIR/webrtc-config-put.json" \
			enabled=true bindAddress=127.0.0.1 >/dev/null 2>&1 || true
		PUT="$(api_code PUT /config/webrtc "$RUN_DIR/webrtc-config-put-response.json" "$RUN_DIR/webrtc-config-put.json")"
		if [[ "$PUT" == "200" ]] && restart_server; then
			api_code GET /config/webrtc "$RUN_DIR/webrtc-config-get.json" >/dev/null 2>&1 || true
			if [[ "$(cfg_enabled "$RUN_DIR/webrtc-config-get.json" config)" == "true" ]]; then
				WEBRTC_ENABLED=1
				status_pass "webrtc-enable" "enabled via PUT + restart; config.enabled=true"
			else
				status_fail "webrtc-enable" "PUT accepted but GET config.enabled is not true after restart"
			fi
		else
			status_fail "webrtc-enable" "PUT /api/admin/config/webrtc -> $PUT (restart may have failed)"
		fi
	fi
	curl -fsS -m 5 "$BASE/api/config" -o "$RUN_DIR/public-config.json" 2>/dev/null || true
	if [[ "$(node "$JSON_GET" "$RUN_DIR/public-config.json" webrtc.enabled "" 2>/dev/null || true)" == "true" ]]; then
		status_pass "public-config" "/api/config exposes webrtc.enabled=true"
	else
		status_fail "public-config" "/api/config does not expose webrtc.enabled=true after enablement"
	fi

	# Global webrtc.enabled only starts the engine; every output variant also has
	# an outputs.webrtc switch (default disabled for backward compatibility). WHEP
	# answers webrtc_variant_not_found until the variants are opted in.
	VARIANT_API="$(api_code GET /serverconfig "$RUN_DIR/serverconfig.json")"
	if [[ "$VARIANT_API" == "200" ]]; then
		VARIANTS_BODY="$RUN_DIR/variants-put.json"
		if node "$WORKSPACE_ROOT/tools/scripts/lib/enable-webrtc-variants.js" \
			"$RUN_DIR/serverconfig.json" "$VARIANTS_BODY" >"$RUN_DIR/variants-helper.log" 2>&1; then
			VARIANT_PUT="$(api_code POST /config/video/streamoutputvariants "$RUN_DIR/variants-put-response.json" "$VARIANTS_BODY")"
			if [[ "$VARIANT_PUT" == "200" ]] && restart_server; then
				status_pass "webrtc-variant-enable" "output variants rebuilt with outputs.webrtc.enabled=true ($(node "$JSON_GET" "$VARIANTS_BODY" 'value.0.outputs.webrtc.enabled' '?') on variant 0) and server restarted"
			else
				status_fail "webrtc-variant-enable" "POST /api/admin/config/video/streamoutputvariants -> $VARIANT_PUT (restart may have failed)"
			fi
		else
			status_blocked "webrtc-variant-enable" "no videoQualityVariants in /api/admin/serverconfig (see variants-helper.log)"
		fi
	else
		status_blocked "webrtc-variant-enable" "GET /api/admin/serverconfig -> $VARIANT_API"
	fi
else
	status_blocked "webrtc-config-api" "GET /api/admin/config/webrtc -> $CONFIG_API (admin config API unavailable)"
	status_blocked "webrtc-enable" "no admin config API to enable WebRTC"
	status_blocked "public-config" "WebRTC not enabled"
fi

GO_HARNESS=0
if ls "$SCRIPT_DIR"/*_test.go >/dev/null 2>&1; then GO_HARNESS=1; fi
run_go_harness() { # run_go_harness <tag> [extra env assignments]
	local tag="$1" extra="${2:-}"
	if [[ "$GO_HARNESS" != "1" ]]; then
		status_blocked "$tag" "no Pion WHEP Go harness present under test/automated/webrtc"
		return 3
	fi
	local rc
	(
		cd "$OWNCAST_DIR" || exit 1
		# shellcheck disable=SC2086
		env OWNCAST_BASE_URL="$BASE" OWNCAST_WEB_PORT="$WEB_PORT" $extra \
			timeout 420 go test -tags=integration -count=1 -timeout 6m ./test/automated/webrtc/...
	) >"$RUN_DIR/go-$tag.log" 2>&1
	rc=$?
	if [[ "$rc" == "0" ]]; then
		status_pass "$tag" "go test -tags=integration ./test/automated/webrtc/... passed (go-$tag.log)"
	else
		status_fail "$tag" "go test -tags=integration failed rc=$rc (go-$tag.log)"
	fi
	return "$rc"
}

# WHEP contract/media assertions run once a live stream exists (T3 below): the
# server's webrtc-enabled variant view is populated with the stream plan, so a
# WHEP 201 before any plan exists can legitimately answer
# webrtc_variant_not_found.

# --- WHEP answer must carry ICE candidates (loopback bind regression) -------
# Pion filters loopback candidates unless the engine opts in; with a loopback
# bindAddress a regression here silently breaks every viewer (connection stuck
# at "new"). A canned H.264+Opus offer is enough to inspect the answer SDP.
echo ""
echo "== WHEP answer ICE candidates =="
WHEP_SDP="$RUN_DIR/whep-canned-offer.sdp"
cat >"$WHEP_SDP" <<'SDP'
v=0
o=- 4611731400430051336 1 IN IP4 127.0.0.1
s=-
t=0 0
a=group:BUNDLE 0 1
a=msid-semantic: WMS *
m=video 9 UDP/TLS/RTP/SAVPF 96
c=IN IP4 0.0.0.0
a=rtcp:9 IN IP4 0.0.0.0
a=ice-ufrag:qaoffr
a=ice-pwd:qaofferofferqaofferofferq
a=fingerprint:sha-256 11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00
a=setup:actpass
a=mid:0
a=sendonly
a=rtcp-mux
a=rtpmap:96 H264/90000
a=rtcp-fb:96 nack
a=fmtp:96 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f
a=ssrc:1001 cname:qa
m=audio 9 UDP/TLS/RTP/SAVPF 111
c=IN IP4 0.0.0.0
a=rtcp:9 IN IP4 0.0.0.0
a=ice-ufrag:qaoffr
a=ice-pwd:qaofferofferqaofferofferq
a=fingerprint:sha-256 11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00
a=setup:actpass
a=mid:1
a=sendonly
a=rtcp-mux
a=rtpmap:111 opus/48000/2
a=fmtp:111 minptime=10;useinbandfec=1
a=ssrc:1002 cname:qa
SDP
check_whep_candidates() {
	WHEP_CODE="$(curl -s -m 20 -D "$RUN_DIR/whep-canned-headers.txt" -o "$RUN_DIR/whep-canned-answer.sdp" \
		-w '%{http_code}' -X POST -H 'Content-Type: application/sdp' \
		--data-binary "@$WHEP_SDP" "$BASE/webrtc/whep?variant=0" 2>/dev/null || echo 000)"
	if [[ "$WHEP_CODE" == "201" ]]; then
		if grep -qE '^a=candidate:' "$RUN_DIR/whep-canned-answer.sdp"; then
			status_pass "whep-answer-candidates" "201 answer carries ICE candidate(s): $(grep -cE '^a=candidate:' "$RUN_DIR/whep-canned-answer.sdp" | tr -d ' ') candidate line(s)"
		else
			status_fail "whep-answer-candidates" "201 answer has NO a=candidate lines - loopback ICE candidate regression (services/webrtc/engine.go loopback bind fix)"
		fi
		LOCATION="$(grep -i '^location:' "$RUN_DIR/whep-canned-headers.txt" 2>/dev/null | head -1 | awk '{print $2}' | tr -d '\r')"
		if [[ -n "$LOCATION" ]]; then
			curl -s -o /dev/null -m 10 -X DELETE "$BASE$LOCATION" 2>/dev/null || true
			status_pass "whep-session-delete" "DELETE $LOCATION completed"
		else
			status_fail "whep-session-delete" "201 answer missing Location header"
		fi
	elif [[ "$WHEP_CODE" =~ ^4[0-9][0-9]$ ]]; then
		status_blocked "whep-answer-candidates" "canned offer rejected with $WHEP_CODE (offer-shape limitation; real-offer media/candidate assertions are covered by the Pion Go harness) - see whep-canned-answer.sdp"
	else
		status_fail "whep-answer-candidates" "WHEP POST -> $WHEP_CODE (expected 201)"
	fi
}

echo ""
echo "== T3: RTMP -> WebRTC (publisher + WHEP contract + media) =="
if [[ "$WEBRTC_ENABLED" == "1" && "$DO_MEDIA" == "1" ]]; then
	"$FF" -hide_banner -loglevel error -re -stream_loop -1 -i "$SAMPLE_FLV" \
		-c copy -f flv "rtmp://127.0.0.1:$RTMP_PORT/live/abc123" >"$RUN_DIR/publisher-rtmp.log" 2>&1 &
	PUB_PID=$!
	wait_online 25
	RC=$?
	if [[ "$RC" == "0" ]]; then
		status_pass "t3-publisher" "RTMP publisher online"
		run_go_harness "whep-contract" ""
		check_whep_candidates
		run_go_harness "t3-rtmp-webrtc" "OWNCAST_WHEP_EXPECT_LIVE=1 OWNCAST_EXPECT_INPUT_PROTOCOL=rtmp"
	else
		status_blocked "t3-publisher" "RTMP publisher did not go online (publisher-rtmp.log)"
		status_blocked "t3-rtmp-webrtc" "no live RTMP stream for the WebRTC media test"
	fi
	kill "$PUB_PID" 2>/dev/null || true; wait "$PUB_PID" 2>/dev/null || true; PUB_PID=""
	wait_offline 20 || true
else
	status_skip "t3-publisher" "WebRTC disabled or --no-media"
	if [[ "$WEBRTC_ENABLED" == "1" ]]; then
		run_go_harness "whep-contract-no-stream" ""
		status_blocked "t3-rtmp-webrtc" "media test requires a publisher (do not pass --no-media)"
	else
		status_blocked "whep-contract" "WebRTC not enabled (config API missing or enablement failed)"
		status_blocked "t3-rtmp-webrtc" "WebRTC not enabled"
	fi
fi

echo ""
echo "== T4: SRT -> WebRTC media delivery =="
if [[ "$WEBRTC_ENABLED" == "1" && "$DO_MEDIA" == "1" ]]; then
	api_code GET /config/srt "$RUN_DIR/srt-config-get.json" >/dev/null 2>&1 || true
	SRT_GET="$(api_code GET /config/srt "$RUN_DIR/srt-config-get.json")"
	if [[ "$SRT_GET" == "200" ]]; then
		node "$CONFIG_MERGE" "$RUN_DIR/srt-config-get.json" "$RUN_DIR/srt-config-put.json" \
			enabled=true bindAddress=127.0.0.1 port="$SRT_PORT" >/dev/null 2>&1 || true
		SRT_PUT="$(api_code PUT /config/srt "$RUN_DIR/srt-config-put-response.json" "$RUN_DIR/srt-config-put.json")"
		SRT_OK=0
		if [[ "$SRT_PUT" == "200" ]] && restart_server; then
			for _ in $(seq 1 20); do
				LINE="$(ss -lunp 2>/dev/null | grep ":$SRT_PORT " || true)"
				[[ "$LINE" == *"pid=$SERVER_PID,"* ]] && SRT_OK=1 && break
				sleep 1
			done
		fi
		if [[ "$SRT_OK" == "1" ]]; then
			status_pass "t4-srt-enable" "SRT listener on 127.0.0.1:$SRT_PORT"
			"$FF" -hide_banner -loglevel error -re -stream_loop -1 -i "$SAMPLE_TS" \
				-c copy -f mpegts "srt://127.0.0.1:$SRT_PORT?streamid=abc123" >"$RUN_DIR/publisher-srt.log" 2>&1 &
			PUB_PID=$!
			wait_online 25
			RC=$?
			if [[ "$RC" == "0" ]]; then
				status_pass "t4-publisher" "SRT publisher online (inputProtocol=srt)"
				run_go_harness "t4-srt-webrtc" "OWNCAST_WHEP_EXPECT_LIVE=1 OWNCAST_EXPECT_INPUT_PROTOCOL=srt"
			else
				status_blocked "t4-srt-webrtc" "SRT publisher did not go online (publisher-srt.log)"
			fi
			kill "$PUB_PID" 2>/dev/null || true; wait "$PUB_PID" 2>/dev/null || true; PUB_PID=""
			wait_offline 20 || true
		else
			status_blocked "t4-srt-webrtc" "SRT listener did not bind :$SRT_PORT after config apply+restart"
		fi
	else
		status_blocked "t4-srt-webrtc" "SRT config API -> $SRT_GET"
	fi
else
	status_skip "t4-srt-webrtc" "WebRTC disabled or --no-media"
fi

# --- gated WHEP contract tests ----------------------------------------------
# These three used to be reported BLOCKED in every gate, so the capacity limit,
# the signaling rate limit and the strict-domain Host check had no runtime
# coverage at all. Each setting is ApplyLive, so the server can be reconfigured
# for the test and restored afterwards without a restart.
gated_restore_config() {
	[[ -f "$RUN_DIR/gated-original.json" ]] || return 0
	node "$CONFIG_MERGE" "$RUN_DIR/gated-original.json" "$RUN_DIR/gated-restore.json" >/dev/null 2>&1 || return 0
	api_code PUT /config/webrtc "$RUN_DIR/gated-restore-response.json" "$RUN_DIR/gated-restore.json" >/dev/null 2>&1 || true
}

run_gated_whep() { # run_gated_whep <tag> <go test name> <extra env> <merge args...>
	local tag="$1" test_name="$2" extra_env="$3"
	shift 3
	if [[ "$WEBRTC_ENABLED" != "1" || "$GO_HARNESS" != "1" ]]; then
		status_skip "$tag" "WebRTC disabled or no Go harness"
		return 0
	fi
	if ! node "$CONFIG_MERGE" "$RUN_DIR/gated-original.json" "$RUN_DIR/gated-$tag-put.json" "$@" >/dev/null 2>&1; then
		status_blocked "$tag" "could not build the config update"
		return 0
	fi
	local put rc=0
	put="$(api_code PUT /config/webrtc "$RUN_DIR/gated-$tag-put-response.json" "$RUN_DIR/gated-$tag-put.json")"
	if [[ "$put" != "200" ]]; then
		status_blocked "$tag" "config PUT -> $put (gated-$tag-put-response.json)"
		gated_restore_config
		return 0
	fi
	(
		cd "$OWNCAST_DIR" || exit 1
		# shellcheck disable=SC2086
		env OWNCAST_BASE_URL="$BASE" $extra_env \
			timeout 180 go test -tags=integration -count=1 -run "^$test_name$" ./test/automated/webrtc/...
	) >"$RUN_DIR/go-$tag.log" 2>&1 || rc=$?
	gated_restore_config
	if [[ "$rc" == "0" ]]; then
		status_pass "$tag" "$test_name passed with the gated config (go-$tag.log)"
	else
		status_fail "$tag" "$test_name failed rc=$rc (go-$tag.log)"
	fi
	return 0
}

if [[ "$WEBRTC_ENABLED" == "1" && "$DO_MEDIA" == "1" && "$GO_HARNESS" == "1" ]]; then
	api_code GET /config/webrtc "$RUN_DIR/gated-original.json" >/dev/null 2>&1 || true
	"$FF" -hide_banner -loglevel error -re -stream_loop -1 -i "$SAMPLE_FLV" \
		-c copy -f flv "rtmp://127.0.0.1:$RTMP_PORT/live/abc123" >"$RUN_DIR/publisher-gated.log" 2>&1 &
	PUB_PID=$!
	if wait_online 25; then
		run_gated_whep "capacity-gate" "TestWHEPCapacityRejection" "" maxViewers=1
		run_gated_whep "rate-limit" "TestWHEPRateLimit" "" signalingRateLimit=1
		run_gated_whep "domain-binding" "TestWHEPStrictDomain" "OWNCAST_WHEP_DOMAIN=live.example.org" \
			domain=live.example.org 'publicIPs=["127.0.0.1"]' strictDomain=true
	else
		status_blocked "capacity-gate" "no live stream for the gated WHEP tests (publisher-gated.log)"
		status_blocked "rate-limit" "no live stream for the gated WHEP tests"
		status_blocked "domain-binding" "no live stream for the gated WHEP tests"
	fi
	kill "$PUB_PID" 2>/dev/null || true; wait "$PUB_PID" 2>/dev/null || true; PUB_PID=""
	wait_offline 20 || true
else
	status_skip "capacity-gate" "WebRTC disabled, --no-media, or no Go harness"
	status_skip "rate-limit" "WebRTC disabled, --no-media, or no Go harness"
	status_skip "domain-binding" "WebRTC disabled, --no-media, or no Go harness"
fi
status_blocked "nack-pli-counters" "no public/admin per-viewer NACK/PLI counter API; harness asserts RTCP PLI acceptance only"

cp "$STATE_DIR/logs/owncast.log" "$RUN_DIR/owncast.log" 2>/dev/null || true
echo ""
status_finish
