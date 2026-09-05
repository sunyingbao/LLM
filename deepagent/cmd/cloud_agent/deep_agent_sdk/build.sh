#!/bin/bash
set -e

RUN_NAME="ad.creative.deep_agent_sdk"
CURDIR=$(cd "$(dirname "$0")"; pwd)
cd "$CURDIR"

rm -rf output
mkdir -p output/bin output/conf
cp script/bootstrap.sh output 2>/dev/null
chmod +x output/bootstrap.sh
cp script/bootstrap.sh output/bootstrap_staging.sh
chmod +x output/bootstrap_staging.sh
cp -R conf/. output/conf/

go build -o output/bin/${RUN_NAME} .
