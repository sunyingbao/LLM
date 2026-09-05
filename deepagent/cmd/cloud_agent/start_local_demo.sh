#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEV_CONFIG="${DEV_CONFIG:-$HOME/.config/deepagent/dev_config.json}"

python3 "$SCRIPT_DIR/dev.py" --config "$DEV_CONFIG" start
