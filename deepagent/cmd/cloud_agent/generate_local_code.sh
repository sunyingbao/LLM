#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
go_bin="$(go env GOPATH)/bin"
hertztool_bin="${HERTZTOOL_BIN:-$go_bin/hertztool}"
expected_hertztool_version="v3.4.7"

require_tool_version() {
  local tool_name="$1"
  local tool_path="$2"
  local expected_version="$3"
  local actual_version

  if [[ ! -x "$tool_path" ]]; then
    echo "$tool_name is not installed at $tool_path" >&2
    return 1
  fi
  actual_version="$($tool_path --version 2>&1)"
  if [[ "$actual_version" != *"$expected_version"* ]]; then
    echo "$tool_name version mismatch: expected $expected_version, got $actual_version" >&2
    return 1
  fi
}

require_tool_version "hertztool" "$hertztool_bin" "$expected_hertztool_version"

(
  cd "$script_dir/deep_agent_sdk"
  PATH="$go_bin:$PATH" "$hertztool_bin" model \
    --idl ../idl/deep_agent_sdk.thrift \
    --out_dir . \
    --model_dir hertz_gen
  go mod tidy
)

echo "local Hertz code generated successfully"
