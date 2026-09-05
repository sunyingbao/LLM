//go:build !windows

package thread

import (
	"context"
	agentworker "eino-cli/deepagent/worker"
)

type compactOperation struct {
	turnID             string
	consumedMessageIDs []string
	consumedInputsMeta []any
	cancel             context.CancelFunc
	interrupted        bool
	interrupt          agentworker.ThreadInterruptRequest
}
