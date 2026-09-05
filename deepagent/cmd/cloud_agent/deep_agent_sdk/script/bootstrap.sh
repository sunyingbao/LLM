#!/bin/bash
export HERTZTOOL_VERSION=v3.4.7

CURDIR=$(cd $(dirname $0); pwd)
if [ "X$1" != "X" ]; then
	RUNTIME_ROOT=$1
else
	RUNTIME_ROOT=${CURDIR}
fi

if [ "X$RUNTIME_ROOT" == "X" ]; then
	echo "There is no RUNTIME_ROOT support."
	echo "Usage: ./bootstrap.sh $RUNTIME_ROOT"
	exit -1
fi

PORT=$2

RUNTIME_CONF_ROOT=$RUNTIME_ROOT/conf

if [ "${IS_TCE_DOCKER_ENV}" == 1 ] && [ -n "${RUNTIME_LOGDIR}" ]; then
    RUNTIME_LOG_ROOT=$RUNTIME_LOGDIR
else
    RUNTIME_LOG_ROOT=$RUNTIME_ROOT/log
fi

if [ ! -d $RUNTIME_LOG_ROOT/app ]; then
	mkdir -p $RUNTIME_LOG_ROOT/app
fi

if [ ! -d $RUNTIME_LOG_ROOT/rpc ]; then
	mkdir -p $RUNTIME_LOG_ROOT/rpc
fi

if [ "$IS_HOST_NETWORK" == "1" ]; then
	export RUNTIME_SERVICE_PORT=$PORT0
	export RUNTIME_DEBUG_PORT=$PORT1
fi

BinaryName=ad.creative.deep_agent_sdk

export HERTZ_LOG_DIR=$RUNTIME_LOG_ROOT
if [ "X$PORT" != "X" ]; then
	export DEEP_AGENT_SDK_API_ADDRESS=":$PORT"
fi

echo "$CURDIR/bin/${BinaryName}"

exec $CURDIR/bin/${BinaryName}
