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
	turn  *turn
}

func (c *TurnHandle) TurnID() string {
	if c == nil || c.turn == nil {
		return ""
	}
	return c.turn.turnID
}

func (c *TurnHandle) Wait(ctx context.Context) error {
	if c == nil || c.turn == nil {
		return ErrInvalidOp
	}
	select {
	case <-c.turn.done:
		return c.turn.err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *TurnHandle) IsActive() bool {
	if c == nil || c.owner == nil || c.turn == nil {
		return false
	}
	c.owner.mu.Lock()
	defer c.owner.mu.Unlock()
	return c.turn.isActive()
}

func (c *TurnHandle) ConsumedInputs() []*schema.Message {
	if c == nil || c.owner == nil || c.turn == nil {
		return nil
	}
	c.owner.mu.Lock()
	defer c.owner.mu.Unlock()
	if len(c.turn.consumed) == 0 {
		return nil
	}
	return copyMessages(c.turn.consumed)
}

func (c *TurnHandle) ConsumedInputsMeta() []any {
	if c == nil || c.owner == nil || c.turn == nil {
		return nil
	}
	c.owner.mu.Lock()
	defer c.owner.mu.Unlock()
	return copyConsumedInputsMeta(c.turn.consumedInputsMeta)
}
