#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${SCRIPT_DIR}/../../../../../../"
BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
AUTO_START="${AUTO_START_SERVER:-1}"
VISUAL_AUDIT_MOCK="${VISUAL_AUDIT_MOCK:-1}"
if [ -z "${PORT+x}" ]; then
  BASE_HOST_PORT="${BASE_URL#*://}"
  BASE_HOST_PORT="${BASE_HOST_PORT%/}"
  BASE_PORT_FROM_URL="${BASE_HOST_PORT#*:}"
  if [ "$BASE_PORT_FROM_URL" != "$BASE_HOST_PORT" ]; then
    PORT="${BASE_PORT_FROM_URL%%/*}"
  else
    PORT=8080
  fi
else
  PORT="${PORT}"
fi
cd "$ROOT_DIR"

HAS_SERVER=0
SERVER_PID=""

cleanup() {
  if [ -n "${SERVER_PID}" ]; then
    if kill "$SERVER_PID" >/dev/null 2>&1; then
      wait "$SERVER_PID" >/dev/null 2>&1 || true
    fi
  fi
}
trap cleanup EXIT

log() {
  printf "[smoke] %s\n" "$1"
}

check_server() {
  curl -fsS "$BASE_URL" >/dev/null 2>&1
}

if [ "$AUTO_START" = "1" ] && [ "$VISUAL_AUDIT_MOCK" = "0" ]; then
  if ! check_server; then
    log "starting backend server on ${BASE_URL}"
    (
      cd "$ROOT_DIR"
      DEEP_AGENT_SDK_API_ADDRESS="127.0.0.1:$PORT" \
        GOCACHE=/tmp/gocache_llm_webui \
        go run ./deepagent/cmd/cloud_agent/deep_agent_sdk \
        >/tmp/llm_deep_agent_sdk_smoke.log 2>&1
    ) &
    SERVER_PID="$!"

    for _ in $(seq 1 40); do
      if check_server; then
        HAS_SERVER=1
        break
      fi
      sleep 0.25
    done

    if [ "$HAS_SERVER" != "1" ]; then
      echo "---- backend startup log ----" >&2
      cat /tmp/llm_deep_agent_sdk_smoke.log >&2
      exit 1
    fi
  else
    log "backend already running: ${BASE_URL}"
  fi
fi

node --test deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/*.test.mjs
GOCACHE=/tmp/gocache_llm_webui go test ./deepagent/cmd/cloud_agent/deep_agent_sdk/webui
GOCACHE=/tmp/gocache_llm_webui go test ./deepagent/cmd/cloud_agent/deep_agent_sdk/service/changes ./deepagent/cmd/cloud_agent/deep_agent_sdk/service/input ./deepagent/cmd/cloud_agent/deep_agent_sdk/service/timeline ./deepagent/cmd/cloud_agent/deep_agent_sdk/service/session ./deepagent/cmd/cloud_agent/deep_agent_sdk/service/config
if [ "$AUTO_START" = "1" ] && [ "$VISUAL_AUDIT_MOCK" = "0" ] && [ "$HAS_SERVER" = "1" ]; then
  export BASE_URL
  log "backend ready, start visual audit"
fi
export VISUAL_AUDIT_MOCK
VISUAL_AUDIT_STRICT="${VISUAL_AUDIT_STRICT:-0}" \
  node deepagent/cmd/cloud_agent/deep_agent_sdk/webui/visual_audit/run-visual-audit.mjs
