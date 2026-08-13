package main

import (
	"context"

	"eino-cli/deepagent/cloud/worker/bootstrap"
)

func run(ctx context.Context, args []string) error {
	return bootstrap.Run(ctx, bootstrap.Options{Args: args})
}
