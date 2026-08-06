package videoagent

import (
	"context"
	"fmt"
)

// CallbackProcessor turns a durable MQ message into one idempotent Run advance.
type CallbackProcessor struct {
	Runner *Runner
}

func (processor CallbackProcessor) Process(ctx context.Context, message CallbackMessage) error {
	if processor.Runner == nil {
		return fmt.Errorf("callback runner is nil")
	}
	return processor.Runner.ProcessCallback(ctx, message)
}

// ConsumeCallbacks connects any production MQ consumer to the workflow recovery path.
func ConsumeCallbacks(ctx context.Context, consumer MessageConsumer, processor CallbackProcessor) error {
	if consumer == nil {
		return fmt.Errorf("callback consumer is nil")
	}
	return consumer.Consume(ctx, processor.Process)
}
