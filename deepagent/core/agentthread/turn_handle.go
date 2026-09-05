package agentthread

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

// TurnHandle is a read-only view of one current turn owned by DeepAgentThread.
// It can be held after the thread starts another turn; Wait always waits for
// this turn, not for whatever turn becomes current later.
type TurnHandle struct {
	owner *DeepAgentThread
	run   *run
}

func (c *TurnHandle) TurnID() (turnID string) {
	if c == nil || c.run == nil {
		return ""
	}
	return c.run.turnID
}

func (c *TurnHandle) Wait(ctx context.Context) (err error) {
	if c == nil || c.run == nil {
		return ErrInvalidOp
	}
	select {
	case <-c.run.done:
		return c.run.runErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *TurnHandle) IsActive() (active bool) {
	if c == nil || c.owner == nil || c.run == nil {
		return false
	}
	c.owner.mu.Lock()
	defer c.owner.mu.Unlock()
	return c.owner.current == c.run
}

func (c *TurnHandle) ConsumedInputs() (messages []*schema.Message) {
	if c == nil || c.owner == nil || c.run == nil {
		return nil
	}
	c.owner.mu.Lock()
	defer c.owner.mu.Unlock()
	if len(c.run.consumed) == 0 {
		return nil
	}
	messages = copyMessages(c.run.consumed)
	return messages
}

func (c *TurnHandle) ConsumedInputsMeta() (metadata []any) {
	if c == nil || c.owner == nil || c.run == nil {
		return nil
	}
	c.owner.mu.Lock()
	defer c.owner.mu.Unlock()
	metadata = copyConsumedInputsMeta(c.run.consumedInputMeta)
	return metadata
}
