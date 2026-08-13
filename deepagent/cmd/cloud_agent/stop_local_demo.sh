#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_PATH="$(python3 -c 'import os, sys; print(os.path.realpath(sys.argv[1]))' "${BASH_SOURCE[0]}")"
SCRIPT_DIR="$(cd "$(dirname "$SCRIPT_PATH")" && pwd)"
SDK_REPO="$(cd "$SCRIPT_DIR/../.." && pwd)"
RUNTIME_DIR="${CLOUD_AGENT_RUNTIME_DIR:-/tmp/cloud_agent_demo}"
AC_BIN="$RUNTIME_DIR/bin/aic_agent_coordinator"
AC_PID="$RUNTIME_DIR/aic_agent_coordinator.pid"
DEV_CONFIG="${DEV_CONFIG:-$HOME/.config/aic_agent_sdk/dev_config.json}"

log() {
  printf '[cloud-agent-local] %s\n' "$*"
}

stop_cloud_agent() {
  log "stop aic_agent_sdk_api, aic_agent_sdk_worker, aic_agent_sdk_session"
  if [[ -f "$DEV_CONFIG" ]]; then
    python3 "$SCRIPT_DIR/dev.py" --config "$DEV_CONFIG" stop
  else
    python3 "$SCRIPT_DIR/dev.py" stop
  fi
}

stop_agent_coordinator() {
  local pid command
  pid="$(cat "$AC_PID" 2>/dev/null || true)"
  if [[ -z "$pid" ]]; then
    log "agent_coordinator: no managed pid"
    return
  fi
  if ! kill -0 "$pid" >/dev/null 2>&1; then
    log "agent_coordinator: remove stale pid=$pid"
    rm -f "$AC_PID"
    return
  fi
  command="$(ps -p "$pid" -o command= 2>/dev/null || true)"
  if [[ "$command" != "$AC_BIN" ]]; then
    printf '[cloud-agent-local][error] refuse to stop unmanaged pid=%s command=%s\n' "$pid" "$command" >&2
    return 1
  fi

  log "stop agent_coordinator pid=$pid"
  kill -TERM "$pid"
  for _ in {1..50}; do
    if ! kill -0 "$pid" >/dev/null 2>&1; then
      rm -f "$AC_PID"
      return
    fi
    sleep 0.1
  done
  log "force stop agent_coordinator pid=$pid"
  kill -KILL "$pid" 2>/dev/null || true
  rm -f "$AC_PID"
}

main() {
  stop_cloud_agent
  stop_agent_coordinator
  log "Cloud Agent local demo stopped"
}

main "$@"
