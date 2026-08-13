#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
go_bin="$(go env GOPATH)/bin"
hertztool_bin="${HERTZTOOL_BIN:-$go_bin/hertztool}"
kitex_bin="${KITEX_BIN:-$go_bin/kitex}"
expected_hertztool_version="v3.4.7"
expected_kitex_version="v1.22.1"

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
require_tool_version "kitex" "$kitex_bin" "$expected_kitex_version"
if [[ ! -x "$go_bin/thriftgo" ]]; then
  echo "thriftgo is not installed at $go_bin/thriftgo" >&2
  exit 1
fi

(
  cd "$script_dir/aic_agent_sdk_session"
  PATH="$go_bin:$PATH" "$kitex_bin" \
    -module code.byted.org/ad/aic_agent_sdk/cmd/cloud_agent/aic_agent_sdk_session \
    -gen-path kitex_gen \
    -I ../idl \
    ../idl/aic_agent_sdk_session.thrift
  go mod tidy
)

(
  cd "$script_dir/aic_agent_sdk_api"
  PATH="$go_bin:$PATH" "$hertztool_bin" model \
    --idl ../idl/aic_agent_sdk_api.thrift \
    --out_dir . \
    --model_dir hertz_gen
  go mod tidy
)

echo "local Hertz and Kitex code generated successfully"
