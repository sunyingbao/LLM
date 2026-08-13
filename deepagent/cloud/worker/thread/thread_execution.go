//go:build !windows

package thread

import (
	"context"
	"fmt"
	"maps"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/cloud/worker/runtimectx"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/worker"
)

func (t *Runtime) postUserInput(ctx context.Context, cmd userInputCommand) (*agentworker.PostMessageResult, error) {
	opts := []agentthread.SubmitInputOption{}
	if cmd.message != nil && len(cmd.message.Metadata) > 0 {
		opts = append(opts, agentthread.WithInputMeta(maps.Clone(cmd.message.Metadata)))
	}
	opts = append(opts, agentthread.WithTurnRunnerStartHook(func(runCtx context.Context, req agentthread.TurnRunnerStartRequest) context.Context {
		return runtimectx.ContextWithTurnIdentity(runCtx, runtimectx.TurnIdentity{
			ThreadID:  t.threadInfo.ThreadID,
			TurnID:    req.TurnID,
			MessageID: cmd.messageID(),
		})
	}))
	if t.turnRunnerConfig != nil {
		opts = append(opts, agentthread.WithTurnRunnerConfigResolver(func(ctx context.Context, req agentthread.TurnRunnerConfigRequest) (*agentthread.TurnRunnerConfig, error) {
			return t.runnerConfig(ctx, req.TurnID, cmd.message, cmd.mode, false)
		}))
	}
	result, err := t.thread.SubmitInput(ctx, cmd.schema, opts...)
	if err != nil {
		return nil, fmt.Errorf("submit input: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("submit input returned nil result")
	}
	if result.StartNewTurn {
		logs.CtxInfo(ctx, "[cloudagent] turn submitted: 对话流ID=%s thread_id=%s message_id=%s turn_id=%s", t.sessionID, t.threadID, cmd.messageID(), result.TurnID)
		t.waitSubmittedTurn(ctx, result.TurnHandle)
		return &agentworker.PostMessageResult{TurnID: result.TurnID}, nil
	}
	if result.QueuedToActiveTurn {
		return &agentworker.PostMessageResult{TurnID: result.TurnID}, nil
	}
	return nil, fmt.Errorf("submit input returned unknown result: %+v", result)
}

func (t *Runtime) postResumeTurn(ctx context.Context, cmd resumeTurnCommand) (*agentworker.PostMessageResult, error) {
	payload := cmd.payload
	if payload.Approval != nil && payload.Approval.CancelTurn {
		t.emitCancelTurnEvents(ctx, payload)
		return &agentworker.PostMessageResult{TurnID: payload.TurnID}, nil
	}
	if payload.Interrupt != nil {
		logs.CtxInfo(ctx,
			"[cloudagent] interrupt resume received: 对话流ID=%s thread_id=%s turn_id=%s interrupt_id=%s checkpoint_id=%s kind=%s info_type=%s data_bytes=%d",
			t.sessionID, t.threadID, payload.TurnID, payload.InterruptID, payload.CheckpointID, payload.Interrupt.Kind, payload.Interrupt.InfoType, len(payload.Interrupt.Data),
		)
	}
	resumeData, err := t.resumeData(ctx, payload)
	if err != nil {
		if payload.Interrupt != nil {
			logs.CtxError(ctx,
				"[cloudagent] interrupt resume decode failed: 对话流ID=%s thread_id=%s turn_id=%s interrupt_id=%s checkpoint_id=%s kind=%s info_type=%s err=%v",
				t.sessionID, t.threadID, payload.TurnID, payload.InterruptID, payload.CheckpointID, payload.Interrupt.Kind, payload.Interrupt.InfoType, err,
			)
		}
		return nil, err
	}
	if payload.Interrupt != nil {
		logs.CtxInfo(ctx,
			"[cloudagent] interrupt resume decoded: 对话流ID=%s thread_id=%s turn_id=%s interrupt_id=%s checkpoint_id=%s kind=%s info_type=%s resume_data_type=%T",
			t.sessionID, t.threadID, payload.TurnID, payload.InterruptID, payload.CheckpointID, payload.Interrupt.Kind, payload.Interrupt.InfoType, resumeData[payload.InterruptID],
		)
	}
	if payload.Approval != nil && payload.Approval.AllowInSession && payload.Approval.Approved && t.approvalRemember != nil {
		t.approvalRemember.RememberApproval(ctx, payload)
	}

	opts := agentthread.ResumeTurnOptions{
		CheckpointID:       payload.CheckpointID,
		ResumeInterruptIDs: []string{payload.InterruptID},
		ResumeData:         resumeData,
		OnTurnRunnerStart: func(runCtx context.Context, req agentthread.TurnRunnerStartRequest) context.Context {
			return runtimectx.ContextWithTurnIdentity(runCtx, runtimectx.TurnIdentity{
				ThreadID:  t.threadInfo.ThreadID,
				TurnID:    req.TurnID,
				MessageID: cmd.messageID(),
			})
		},
	}
	if t.turnRunnerConfig != nil {
		opts.TurnRunnerConfig = func(ctx context.Context, req agentthread.TurnRunnerConfigRequest) (*agentthread.TurnRunnerConfig, error) {
			return t.runnerConfig(ctx, req.TurnID, cmd.message, cmd.mode, true)
		}
	}
	curTurn, err := t.thread.ResumeTurn(ctx, payload.TurnID, opts)
	if err != nil {
		return nil, fmt.Errorf("resume turn: %w", err)
	}
	t.waitSubmittedTurn(ctx, curTurn)
	return &agentworker.PostMessageResult{TurnID: payload.TurnID}, nil
}

func (t *Runtime) waitSubmittedTurn(ctx context.Context, curTurn *agentthread.TurnHandle) {
	if curTurn == nil {
		logs.CtxError(ctx, "[cloudagent] missing current turn")
		return
	}
	t.mu.Lock()
	claimCtx := t.claimCtx
	t.mu.Unlock()
	if claimCtx == nil {
		claimCtx = ctx
	}
	go func() {
		if err := curTurn.Wait(claimCtx); err != nil && claimCtx.Err() == nil {
			logs.CtxError(claimCtx, "[cloudagent] turn failed: 对话流ID=%s thread_id=%s turn_id=%s err=%v", t.sessionID, t.threadID, curTurn.TurnID(), err)
		}
	}()
}
