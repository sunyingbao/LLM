package deepagents

import "context"

type ContinueAfterModelFunc func(ctx context.Context) (continueRun bool, err error)
