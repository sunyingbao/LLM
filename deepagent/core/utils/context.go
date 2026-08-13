package utils

import (
	"code.byted.org/gopkg/ctxvalues"
	"code.byted.org/gopkg/logs/v2"
	"context"
	"runtime"
)

func NewCtxWithLogID(ctx context.Context) context.Context {
	logId, exists := ctxvalues.LogID(ctx)
	newCtx := context.Background()
	if exists {
		return ctxvalues.SetLogID(newCtx, logId)
	}
	return newCtx
}

func NewCancelCtxWithLogID(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(NewCtxWithLogID(ctx))
}

func PanicGuard(ctx context.Context) {
	if err := recover(); err != nil {
		const size = 64 << 10
		buf := make([]byte, size)
		buf = buf[:runtime.Stack(buf, false)]
		logs.CtxError(ctx, "panic recover: %v\n%s", err, string(buf))
	}
}
