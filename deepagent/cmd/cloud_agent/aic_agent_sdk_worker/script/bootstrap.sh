#!/usr/bin/env bash
set -euo pipefail

CURDIR="$(cd "$(dirname "$0")" && pwd)"
RUNTIME_ROOT="${1:-$CURDIR}"

export PSM="${PSM:-ad.creative.aic_agent_sdk_worker}"

if [[ -n "${RUNTIME_LOGDIR:-}" ]]; then
  mkdir -p "$RUNTIME_LOGDIR/app" "$RUNTIME_LOGDIR/rpc"
fi

cd "$CURDIR"
if [[ -n "${AGENT_WORKER_CONF:-}" ]]; then
  exec "$CURDIR/bin/ad.creative.aic_agent_sdk_worker" -conf "$AGENT_WORKER_CONF"
fi
exec "$CURDIR/bin/ad.creative.aic_agent_sdk_worker"
