#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT_DIR="$ROOT_DIR/output"

usage() {
  cat >&2 <<'EOF'
Usage:
  CUSTOM_ARTIFACT=aic_agent_sdk_api ./build.sh
  CUSTOM_ARTIFACT=aic_agent_sdk_session ./build.sh
  CUSTOM_ARTIFACT=aic_agent_sdk_worker ./build.sh
EOF
}

artifact="${CUSTOM_ARTIFACT:-}"
case "$artifact" in
  aic_agent_sdk_api)
    service_dir="$ROOT_DIR/cmd/cloud_agent/aic_agent_sdk_api"
    ;;
  aic_agent_sdk_session)
    service_dir="$ROOT_DIR/cmd/cloud_agent/aic_agent_sdk_session"
    ;;
  aic_agent_sdk_worker)
    service_dir="$ROOT_DIR/cmd/cloud_agent/aic_agent_sdk_worker"
    ;;
  *)
    usage
    exit 2
    ;;
esac

rm -rf "$OUTPUT_DIR" "$service_dir/output"
(
  cd "$service_dir"
  ./build.sh
)
mv "$service_dir/output" "$OUTPUT_DIR"

echo "Built $artifact into $OUTPUT_DIR"
