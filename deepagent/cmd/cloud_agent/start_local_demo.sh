#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_PATH="$(python3 -c 'import os, sys; print(os.path.realpath(sys.argv[1]))' "${BASH_SOURCE[0]}")"
SCRIPT_DIR="$(cd "$(dirname "$SCRIPT_PATH")" && pwd)"
SDK_REPO="$(cd "$SCRIPT_DIR/../.." && pwd)"

if [[ -n "${AC_REPO:-}" ]]; then
  AC_REPO="$(cd "$AC_REPO" && pwd)"
elif [[ -d "$SDK_REPO/../aic_agent_coordinator" ]]; then
  AC_REPO="$(cd "$SDK_REPO/../aic_agent_coordinator" && pwd)"
else
  printf '[cloud-agent-local][error] cannot find aic_agent_coordinator next to %s\n' "$SDK_REPO" >&2
  exit 1
fi

RUNTIME_DIR="${CLOUD_AGENT_RUNTIME_DIR:-/tmp/cloud_agent_demo}"
AC_LOG_DIR="${AC_LOG_DIR:-/tmp/aic_agent_coordinator_kitex_log}"
AC_BIN="$RUNTIME_DIR/bin/aic_agent_coordinator"
AC_SELFTEST_BIN="$RUNTIME_DIR/bin/aic_agent_coordinator_selftest"
AC_LOG="$RUNTIME_DIR/aic_agent_coordinator.log"
AC_PID="$RUNTIME_DIR/aic_agent_coordinator.pid"
AC_CONFIG="${AIC_AGENT_COORDINATOR_CONFIG:-$RUNTIME_DIR/aic_agent_coordinator.local.yml}"
DEV_CONFIG="${DEV_CONFIG:-$HOME/.config/aic_agent_sdk/dev_config.json}"
MODEL_ENV_FILE="${MODEL_ENV_FILE:-$HOME/.config/aic_agent_sdk/model.env}"
MODEL_SOURCE="${CLOUD_AGENT_MODEL_SOURCE:-auto}"

MYSQL_HOST="${MYSQL_HOST:-127.0.0.1}"
MYSQL_PORT="${MYSQL_PORT:-3306}"
MYSQL_USER="${MYSQL_USER:-ac_test}"
MYSQL_PASSWORD="${MYSQL_PASSWORD:-ac_test_pwd_20260416}"
MYSQL_ADMIN_USER="${MYSQL_ADMIN_USER:-root}"
MYSQL_ADMIN_PASSWORD="${MYSQL_ADMIN_PASSWORD:-}"
MYSQL_DB="${MYSQL_DB:-agent_coordinator_test}"
MYSQL_DSN="${MYSQL_DSN:-${MYSQL_USER}:${MYSQL_PASSWORD}@tcp(${MYSQL_HOST}:${MYSQL_PORT})/${MYSQL_DB}?charset=utf8mb4&parseTime=True&loc=Local}"
REDIS_ADDR="${REDIS_ADDR:-127.0.0.1:6379}"
REDIS_PASSWORD="${REDIS_PASSWORD:-}"
REDIS_DB="${REDIS_DB:-0}"
AC_HOSTPORT="${AC_HOSTPORT:-127.0.0.1:8888}"
AC_NAMESPACE="${AC_NAMESPACE:-cloud_agent}"

log() {
  printf '[cloud-agent-local] %s\n' "$*"
}

die() {
  printf '[cloud-agent-local][error] %s\n' "$*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing command: $1"
}

split_host_port() {
  local hostport="$1"
  local default_port="$2"
  if [[ "$hostport" == *:* ]]; then
    printf '%s %s\n' "${hostport%:*}" "${hostport##*:}"
  else
    printf '%s %s\n' "$hostport" "$default_port"
  fi
}

tcp_open() {
  python3 - "$1" "$2" <<'PY'
import socket
import sys

try:
    with socket.create_connection((sys.argv[1], int(sys.argv[2])), timeout=0.5):
        pass
except OSError:
    raise SystemExit(1)
PY
}

wait_tcp() {
  local name="$1"
  local host="$2"
  local port="$3"
  local timeout="${4:-60}"
  local started
  started="$(date +%s)"
  until tcp_open "$host" "$port" >/dev/null 2>&1; do
    if (( "$(date +%s)" - started >= timeout )); then
      return 1
    fi
    sleep 1
  done
  log "$name ready at $host:$port"
}

zshrc_value() {
  local key="$1"
  [[ -f "$HOME/.zshrc" ]] || return 0
  sed -n "s/^[[:space:]]*\(export[[:space:]]*\)\{0,1\}${key}=\"\([^\"]*\)\".*/\2/p" "$HOME/.zshrc" | head -1
}

load_model_environment() {
  if [[ -n "$MODEL_ENV_FILE" ]]; then
    [[ -f "$MODEL_ENV_FILE" ]] || die "model env file not found: $MODEL_ENV_FILE"
    unset OPENROUTER_API_KEY OPENROUTER_MODEL KIMI_API_KEY KIMI_BASE_URL KIMI_MODEL KIMI_LOG_ID OPENAI_API_KEY OPENAI_BASE_URL OPENAI_MODEL ARK_API_KEY ARK_MODEL
    set -a
    # shellcheck disable=SC1090
    source "$MODEL_ENV_FILE"
    set +a
    MODEL_SOURCE="file"
  fi

  if [[ "$MODEL_SOURCE" == "auto" || "$MODEL_SOURCE" == "super-relay" ]]; then
    local relay_url relay_token relay_model
    relay_url="$(zshrc_value ANTHROPIC_BASE_URL)"
    relay_token="$(zshrc_value ANTHROPIC_AUTH_TOKEN)"
    relay_model="$(zshrc_value ANTHROPIC_MODEL)"
    if [[ -n "$relay_url" && -n "$relay_token" && -n "$relay_model" ]]; then
      export OPENAI_API_KEY="$relay_token"
      export OPENAI_MODEL="$relay_model"
      relay_url="${relay_url%/}"
      [[ "$relay_url" == */v1 ]] || relay_url="$relay_url/v1"
      export OPENAI_BASE_URL="$relay_url"
      unset OPENROUTER_API_KEY OPENROUTER_MODEL KIMI_API_KEY KIMI_MODEL KIMI_BASE_URL KIMI_LOG_ID ARK_API_KEY ARK_MODEL
      MODEL_SOURCE="super-relay"
    elif [[ "$MODEL_SOURCE" == "super-relay" ]]; then
      die "claude-relay settings not found in $HOME/.zshrc"
    fi
  fi

  if [[ -n "${OPENROUTER_API_KEY:-}" ]]; then
    log "model provider: OpenRouter"
  elif [[ -n "${KIMI_API_KEY:-}" ]]; then
    log "model provider: Kimi/ModelHub"
  elif [[ -n "${OPENAI_API_KEY:-}" ]]; then
    log "model provider: OpenAI-compatible ($MODEL_SOURCE)"
  elif [[ -n "${ARK_MODEL:-}" && -n "${ARK_API_KEY:-}" ]]; then
    log "model provider: Ark"
  else
    die "model environment missing; set MODEL_ENV_FILE or configure a supported provider"
  fi
}

load_fornax_environment() {
  if [[ -z "${FORNAX_SPACE_AK:-}" ]]; then
    export FORNAX_SPACE_AK="$(zshrc_value FORNAX_SPACE_AK)"
  fi
  if [[ -z "${FORNAX_SPACE_SK:-}" ]]; then
    export FORNAX_SPACE_SK="$(zshrc_value FORNAX_SPACE_SK)"
  fi
  if [[ -z "${FORNAX_CUSTOM_REGION:-}" ]]; then
    export FORNAX_CUSTOM_REGION="$(zshrc_value FORNAX_CUSTOM_REGION)"
  fi
}

mysql_exec() {
  MYSQL_PWD="$MYSQL_PASSWORD" mysql -h"$MYSQL_HOST" -P"$MYSQL_PORT" -u"$MYSQL_USER" "$MYSQL_DB" "$@"
}

mysql_admin_exec() {
  MYSQL_PWD="$MYSQL_ADMIN_PASSWORD" mysql -h"$MYSQL_HOST" -P"$MYSQL_PORT" -u"$MYSQL_ADMIN_USER" "$@"
}

ensure_local_database() {
  [[ "$MYSQL_DB" =~ ^[A-Za-z0-9_]+$ ]] || die "invalid MySQL database name: $MYSQL_DB"
  [[ "$MYSQL_USER" =~ ^[A-Za-z0-9_.-]+$ ]] || die "invalid MySQL user: $MYSQL_USER"
  mysql_admin_exec -e "
    CREATE DATABASE IF NOT EXISTS \`${MYSQL_DB}\` DEFAULT CHARACTER SET utf8mb4;
    GRANT ALL PRIVILEGES ON \`${MYSQL_DB}\`.* TO '${MYSQL_USER}'@'%';
    GRANT ALL PRIVILEGES ON \`${MYSQL_DB}\`.* TO '${MYSQL_USER}'@'localhost';
  " >/dev/null || die "cannot create MySQL database $MYSQL_DB with admin user $MYSQL_ADMIN_USER"
}

mysql_value() {
  mysql_exec -N -B -e "$1" 2>/dev/null | head -1
}

import_table_if_missing() {
  local table="$1"
  local sql_file="$2"
  if [[ "$(mysql_value "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='${MYSQL_DB}' AND table_name='${table}'")" == "0" ]]; then
    log "create table: $table"
    mysql_exec < "$sql_file"
  fi
}

init_control_plane_schema() {
  log "ensure agent_coordinator schema"
  import_table_if_missing t_agent_namespace "$AC_REPO/sql/t_agent_namespace.sql"
  import_table_if_missing t_thread "$AC_REPO/sql/t_thread.sql"
  import_table_if_missing t_mailbox_message "$AC_REPO/sql/t_mailbox_message.sql"
  import_table_if_missing t_event_log "$AC_REPO/sql/t_event_log.sql"

  if [[ "$(mysql_value "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema='${MYSQL_DB}' AND table_name='t_mailbox_message' AND column_name='trigger_turn_id'")" == "0" ]]; then
    log "migrate t_mailbox_message.trigger_turn_id"
    mysql_exec -e "ALTER TABLE t_mailbox_message ADD COLUMN trigger_turn_id VARCHAR(128) NULL COMMENT 'turn id triggered by this message ack' AFTER handled_at"
  fi
  if [[ "$(mysql_value "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema='${MYSQL_DB}' AND table_name='t_event_log' AND column_name='in_thread_seq'")" == "0" ]]; then
    log "migrate t_event_log.in_thread_seq"
    mysql_exec -e "ALTER TABLE t_event_log ADD COLUMN in_thread_seq BIGINT NOT NULL DEFAULT 0 COMMENT 'producer-provided sequence within owner thread' AFTER thread_id"
  fi
  if [[ "$(mysql_value "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema='${MYSQL_DB}' AND table_name='t_thread' AND index_name='idx_namespace_lease_scan'")" == "0" ]]; then
    log "add index t_thread.idx_namespace_lease_scan"
    mysql_exec -e "ALTER TABLE t_thread ADD KEY idx_namespace_lease_scan (namespace, env, status, lease_deadline_at)"
  fi
  if [[ "$(mysql_value "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema='${MYSQL_DB}' AND table_name='t_event_log' AND index_name='idx_namespace_session_created_event'")" == "0" ]]; then
    log "add index t_event_log.idx_namespace_session_created_event"
    mysql_exec -e "ALTER TABLE t_event_log ADD KEY idx_namespace_session_created_event (namespace, session_id, created_at, event_id)"
  fi
  if [[ "$(mysql_value "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema='${MYSQL_DB}' AND table_name='t_event_log' AND index_name='idx_thread_created_seq'")" == "0" ]]; then
    mysql_exec -e "ALTER TABLE t_event_log ADD KEY idx_thread_created_seq (thread_id, created_at, in_thread_seq)"
  fi
  if [[ "$(mysql_value "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema='${MYSQL_DB}' AND table_name='t_event_log' AND index_name='idx_thread_turn_created_seq'")" == "0" ]]; then
    mysql_exec -e "ALTER TABLE t_event_log ADD KEY idx_thread_turn_created_seq (thread_id, turn_id, created_at, in_thread_seq)"
  fi
}

write_dev_config() {
  if [[ ! -f "$DEV_CONFIG" ]]; then
    python3 "$SCRIPT_DIR/dev.py" --config "$DEV_CONFIG" configure --defaults --force
  fi
  DEV_CONFIG="$DEV_CONFIG" MYSQL_DSN="$MYSQL_DSN" REDIS_ADDR="$REDIS_ADDR" REDIS_PASSWORD="$REDIS_PASSWORD" REDIS_DB="$REDIS_DB" AC_HOSTPORT="$AC_HOSTPORT" AC_NAMESPACE="$AC_NAMESPACE" MODEL_ENV_FILE="$MODEL_ENV_FILE" python3 - <<'PY'
import json
import os
from pathlib import Path

path = Path(os.environ["DEV_CONFIG"]).expanduser()
cfg = json.loads(path.read_text())
cfg["mysql_dsn"] = os.environ["MYSQL_DSN"]
cfg["redis"] = {
    "addr": os.environ["REDIS_ADDR"],
    "password": os.environ.get("REDIS_PASSWORD", ""),
    "db": int(os.environ.get("REDIS_DB", "0") or "0"),
}
cfg["model_env"] = {
    "mode": "file" if os.environ.get("MODEL_ENV_FILE") else "shell",
    "file": os.environ.get("MODEL_ENV_FILE", ""),
}
coordinator = cfg.setdefault("agent_coordinator", {})
coordinator.update({
    "psm": "ad.creative.aic_agent_coordinator",
    "mode": "direct",
    "namespace": os.environ["AC_NAMESPACE"],
    "hostports": [os.environ["AC_HOSTPORT"]],
    "cluster": "",
})
path.parent.mkdir(parents=True, exist_ok=True)
path.write_text(json.dumps(cfg, indent=2, ensure_ascii=False) + "\n")
PY
}

write_agent_coordinator_config() {
  if [[ -n "${AIC_AGENT_COORDINATOR_CONFIG:-}" ]]; then
    [[ -f "$AC_CONFIG" ]] || die "agent_coordinator config not found: $AC_CONFIG"
    log "use agent_coordinator config: $AC_CONFIG"
    return
  fi

  AC_CONFIG="$AC_CONFIG" MYSQL_DSN="$MYSQL_DSN" MYSQL_DB="$MYSQL_DB" REDIS_ADDR="$REDIS_ADDR" REDIS_PASSWORD="$REDIS_PASSWORD" REDIS_DB="$REDIS_DB" python3 - <<'PY'
import json
import os
from pathlib import Path

path = Path(os.environ["AC_CONFIG"]).expanduser()
config = {
    "mysql": {
        "dsn": os.environ["MYSQL_DSN"],
        "db_name": os.environ["MYSQL_DB"],
    },
    "abase": {
        "addr": os.environ["REDIS_ADDR"],
        "password": os.environ.get("REDIS_PASSWORD", ""),
        "db": int(os.environ.get("REDIS_DB", "0") or "0"),
    },
}
path.parent.mkdir(parents=True, exist_ok=True)
path.write_text(json.dumps(config, indent=2, ensure_ascii=False) + "\n")
path.chmod(0o600)
PY
  log "generated agent_coordinator config: $AC_CONFIG"
}

check_dependencies() {
  for command in go python3 mysql redis-cli; do
    need_cmd "$command"
  done
  tcp_open "$MYSQL_HOST" "$MYSQL_PORT" >/dev/null 2>&1 || die "MySQL is not listening at $MYSQL_HOST:$MYSQL_PORT"
  ensure_local_database
  mysql_exec -e "SELECT 1" >/dev/null || die "cannot access MySQL database $MYSQL_DB"
  local redis_host redis_port
  read -r redis_host redis_port < <(split_host_port "$REDIS_ADDR" 6379)
  if [[ -n "$REDIS_PASSWORD" ]]; then
    redis-cli -h "$redis_host" -p "$redis_port" -a "$REDIS_PASSWORD" -n "$REDIS_DB" ping >/dev/null
  else
    redis-cli -h "$redis_host" -p "$redis_port" -n "$REDIS_DB" ping >/dev/null
  fi
}

start_agent_coordinator() {
  local ac_host ac_port pid
  read -r ac_host ac_port < <(split_host_port "$AC_HOSTPORT" 8888)
  if tcp_open "$ac_host" "$ac_port" >/dev/null 2>&1; then
    pid="$(cat "$AC_PID" 2>/dev/null || true)"
    if [[ -n "$pid" && -x "$AC_BIN" && "$(ps -p "$pid" -o command= 2>/dev/null || true)" == "$AC_BIN" ]]; then
      log "agent_coordinator already running pid=$pid"
      return
    fi
    die "$AC_HOSTPORT is occupied by an unmanaged process; stop it explicitly before quick start"
  fi

  mkdir -p "$RUNTIME_DIR/bin" "$AC_LOG_DIR"
  rm -f "$AC_PID"
  log "build agent_coordinator"
  (cd "$AC_REPO" && go build -o "$AC_BIN" .)
  log "start agent_coordinator"
  AIC_AGENT_COORDINATOR_CONFIG="$AC_CONFIG" AC_REPO="$AC_REPO" AC_BIN="$AC_BIN" AC_LOG="$AC_LOG" AC_PID="$AC_PID" AC_LOG_DIR="$AC_LOG_DIR" python3 - <<'PY'
import os
import subprocess
from pathlib import Path

log_file = Path(os.environ["AC_LOG"]).open("ab")
env = os.environ.copy()
env["KITEX_CONFIG_SOURCE"] = "file"
env["KITEX_LOG_DIR"] = os.environ["AC_LOG_DIR"]
proc = subprocess.Popen(
    [os.environ["AC_BIN"]],
    cwd=os.environ["AC_REPO"],
    env=env,
    stdout=log_file,
    stderr=subprocess.STDOUT,
    start_new_session=True,
)
Path(os.environ["AC_PID"]).write_text(f"{proc.pid}\n")
PY
  if ! wait_tcp agent_coordinator "$ac_host" "$ac_port" 60; then
    tail -n 120 "$AC_LOG" >&2 || true
    die "agent_coordinator did not become ready"
  fi
}

register_local_namespace() {
  log "build agent_coordinator namespace client"
  (cd "$AC_REPO" && go build -o "$AC_SELFTEST_BIN" ./cmd/selftest)
  log "register local namespace: $AC_NAMESPACE"
  "$AC_SELFTEST_BIN" \
    -addr "$AC_HOSTPORT" \
    -timeout 20s \
    -namespace "$AC_NAMESPACE" \
    -case register
}

start_cloud_agent() {
  cd "$SDK_REPO"
  python3 cmd/cloud_agent/dev.py --config "$DEV_CONFIG" stop || true
  python3 cmd/cloud_agent/dev.py --config "$DEV_CONFIG" doctor
  python3 cmd/cloud_agent/dev.py --config "$DEV_CONFIG" init-db
  log "audit local MySQL schema against current DDL"
  AC_REPO="$AC_REPO" MYSQL_HOST="$MYSQL_HOST" MYSQL_PORT="$MYSQL_PORT" MYSQL_USER="$MYSQL_USER" MYSQL_PASSWORD="$MYSQL_PASSWORD" MYSQL_DB="$MYSQL_DB" \
    python3 cmd/cloud_agent/audit_local_schema.py --database "$MYSQL_DB" --json-out "$RUNTIME_DIR/schema_audit.json"
  log "audit local MySQL data and Redis runtime state"
  MYSQL_HOST="$MYSQL_HOST" MYSQL_PORT="$MYSQL_PORT" MYSQL_USER="$MYSQL_USER" MYSQL_PASSWORD="$MYSQL_PASSWORD" MYSQL_DB="$MYSQL_DB" \
    REDIS_ADDR="$REDIS_ADDR" REDIS_PASSWORD="$REDIS_PASSWORD" REDIS_DB="$REDIS_DB" \
    python3 cmd/cloud_agent/audit_local_data.py --database "$MYSQL_DB" --json-out "$RUNTIME_DIR/data_audit.json"
  python3 cmd/cloud_agent/dev.py --config "$DEV_CONFIG" start
  python3 cmd/cloud_agent/dev.py --config "$DEV_CONFIG" status
  python3 cmd/cloud_agent/dev.py --config "$DEV_CONFIG" smoke
}

main() {
  log "SDK_REPO=$SDK_REPO"
  log "AC_REPO=$AC_REPO"
  load_model_environment
  load_fornax_environment
  check_dependencies
  init_control_plane_schema
  write_dev_config
  write_agent_coordinator_config
  start_agent_coordinator
  register_local_namespace
  start_cloud_agent
  printf '\nCloud Agent ready: http://127.0.0.1:6789/\n'
  printf 'Start command: cloud-agent-start\n'
  printf 'Stop command:  cloud-agent-stop\n'
  printf 'Absolute stop: %s/stop_local_demo.sh\n' "$SCRIPT_DIR"
}

main "$@"
