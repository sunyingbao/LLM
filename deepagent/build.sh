#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT_DIR="$ROOT_DIR/output"

rm -rf "$OUTPUT_DIR"
mkdir -p "$OUTPUT_DIR/bin" "$OUTPUT_DIR/conf"

(
  cd "$ROOT_DIR/.."
  go build -o "$OUTPUT_DIR/bin/cloud_agent" ./deepagent/cmd/cloud_agent
)
cp -R "$ROOT_DIR/cmd/cloud_agent/conf/." "$OUTPUT_DIR/conf/"

echo "Built cloud_agent into $OUTPUT_DIR"
