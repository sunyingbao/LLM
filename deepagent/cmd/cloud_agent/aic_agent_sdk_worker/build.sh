#!/usr/bin/env bash
set -euo pipefail

RUN_NAME="ad.creative.aic_agent_sdk_worker"
CURDIR="$(cd "$(dirname "$0")"; pwd)"
cd "$CURDIR"

rm -rf output
mkdir -p output/bin output/conf
cp script/* output/
cp -R conf/. output/conf/
chmod +x output/bootstrap.sh

go build -o "output/bin/${RUN_NAME}" .
