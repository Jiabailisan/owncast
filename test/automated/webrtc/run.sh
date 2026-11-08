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
WORKSPACE_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
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
	exec unshare -rn env QA_IN_NETNS=1 bash -c 'ip link set lo up 2>/dev/null || true; exec bash "$@"' _ "$0" "$@"
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
		node "$CONFIG_MERGE" "$RUN_DIR/webrtc-config-get.json" "$RUN_DIR/webrtc-config-put.json" enabled=true >/dev/null 2>&1 || true
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

echo ""
echo "== WHEP contract (Pion Go harness) =="
if [[ "$WEBRTC_ENABLED" == "1" ]]; then
	run_go_harness "whep-contract" ""
else
	status_blocked "whep-contract" "WebRTC not enabled (config API missing or enablement failed)"
fi

echo ""
echo "== T3: RTMP -> WebRTC media delivery =="
if [[ "$WEBRTC_ENABLED" == "1" && "$DO_MEDIA" == "1" ]]; then
	"$FF" -hide_banner -loglevel error -re -stream_loop -1 -i "$SAMPLE_FLV" \
		-c copy -f flv "rtmp://127.0.0.1:$RTMP_PORT/live/abc123" >"$RUN_DIR/publisher-rtmp.log" 2>&1 &
	PUB_PID=$!
	wait_online 25
	RC=$?
	if [[ "$RC" == "0" ]]; then
		status_pass "t3-publisher" "RTMP publisher online"
		run_go_harness "t3-rtmp-webrtc" "OWNCAST_WHEP_EXPECT_LIVE=1 OWNCAST_EXPECT_INPUT_PROTOCOL=rtmp"
	else
		status_blocked "t3-publisher" "RTMP publisher did not go online (publisher-rtmp.log)"
		status_blocked "t3-rtmp-webrtc" "no live RTMP stream for the WebRTC media test"
	fi
	kill "$PUB_PID" 2>/dev/null || true; wait "$PUB_PID" 2>/dev/null || true; PUB_PID=""
	wait_offline 20 || true
else
	status_skip "t3-rtmp-webrtc" "WebRTC disabled or --no-media"
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

status_blocked "capacity-gate" "env-gated (OWNCAST_WHEP_CAPACITY_TEST): requires maxViewers=1 server config; covered by services/webrtc tests"
status_blocked "rate-limit" "env-gated (OWNCAST_WHEP_RATELIMIT_TEST): requires signalingRateLimit=1 server config"
status_blocked "domain-binding" "env-gated (OWNCAST_WHEP_STRICT_DOMAIN_TEST): requires strictDomain=true server config"
status_blocked "nack-pli-counters" "no public/admin per-viewer NACK/PLI counter API; harness asserts RTCP PLI acceptance only"

cp "$STATE_DIR/logs/owncast.log" "$RUN_DIR/owncast.log" 2>/dev/null || true
echo ""
status_finish
