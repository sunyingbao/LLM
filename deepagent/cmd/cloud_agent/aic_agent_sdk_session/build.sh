#!/usr/bin/env bash
set -e

RUN_NAME="ad.creative.aic_agent_sdk_session"
CURDIR=$(cd "$(dirname "$0")"; pwd)
cd "$CURDIR"

rm -rf output
mkdir -p output/bin output/conf
cp script/* output/
cp -R conf/. output/conf/
chmod +x output/bootstrap.sh

if [ "$IS_SYSTEM_TEST_ENV" != "1" ]; then
    go build -o output/bin/${RUN_NAME} .
else
    go test -c -covermode=set -o output/bin/${RUN_NAME} -coverpkg=./... .
fi
