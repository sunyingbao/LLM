package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/cloud/protocol/timeline"
	clientruntime "eino-cli/deepagent/host/runtime"
	tea "github.com/charmbracelet/bubbletea"

	runtimecontext "eino-cli/deepagent/host/executioncontext"
	runtimeRun "eino-cli/deepagent/host/run"
)

type chunkMsg string
type timelineMsg struct{ event timeline.Event }

type doneMsg struct {
	output string
	err    error
}

type resumeResult struct {
	err error
}

func startStream(runtime clientruntime.InteractiveRuntime, sessionID, prompt string, runs *runtimeRun.Manager) (messages <-chan tea.Msg, cancel context.CancelFunc) {
	streamMessages := make(chan tea.Msg, 64)
	ctx := runtimecontext.WithSessionID(context.Background(), sessionID)
	handle, err := runs.Begin(ctx, prompt)
	if err != nil {
		streamMessages <- doneMsg{err: err}
		close(streamMessages)
		return streamMessages, func() {}
	}
	turnStream, err := runtime.StartTurn(handle.Context, prompt)
	if err != nil {
		handle.Complete(runtimeRun.Error, "", err)
		streamMessages <- doneMsg{err: err}
		close(streamMessages)
		return streamMessages, handle.Cancel
	}
	var cancelOnce sync.Once
	cancel = func() {
		cancelOnce.Do(func() {
			handle.Cancel()
			_ = turnStream.Stop(context.Background())
			_ = turnStream.Close()
		})
	}
	go consumeTimeline(runtime, handle, turnStream, streamMessages)
	return streamMessages, cancel
}

func consumeTimeline(runtime clientruntime.InteractiveRuntime, handle *runtimeRun.Handle, stream *clientruntime.TurnStream, messages chan<- tea.Msg) {
	defer close(messages)
	defer func() { _ = stream.Close() }()
	var output strings.Builder
	sawDelta := false
	resumeResults := make(chan resumeResult)
	for {
		select {
		case <-handle.Context.Done():
			handle.Complete(runtimeRun.Interrupted, output.String(), handle.Context.Err())
			messages <- doneMsg{err: handle.Context.Err()}
			return
		case result := <-resumeResults:
			if result.err != nil {
				handle.Complete(runtimeRun.Error, output.String(), result.err)
				messages <- doneMsg{err: result.err}
				return
			}
		case event, ok := <-stream.Events:
			if !ok {
				err := stream.Err()
				if err == nil {
					err = fmt.Errorf("timeline closed before turn completed")
				}
				handle.Complete(runtimeRun.Error, output.String(), err)
				messages <- doneMsg{err: err}
				return
			}
			if !stream.AcceptEvent(event) {
				continue
			}
			eventType := protoevent.EventType(event.EventType)
			switch eventType {
			case protoevent.EventTypeAssistantDelta:
				var payload protoevent.AssistantDeltaEventPayload
				if err := json.Unmarshal(event.Payload, &payload); err != nil {
					messages <- doneMsg{err: err}
					return
				}
				sawDelta = true
				output.WriteString(payload.Delta)
				messages <- chunkMsg(payload.Delta)
			case protoevent.EventTypeAssistantMessage:
				if !sawDelta {
					text := assistantText(event.Payload)
					output.WriteString(text)
					messages <- chunkMsg(text)
				}
				messages <- timelineMsg{event: event}
			case protoevent.EventTypeApprovalRequired:
				messages <- timelineMsg{event: event}
				requestApprovalResume(handle.Context, runtime, stream, event, messages, resumeResults)
			case protoevent.EventTypeTurnFinished:
				messages <- timelineMsg{event: event}
				handle.Complete(runtimeRun.Success, output.String(), nil)
				_ = handle.SaveWorkspaceSnapshot(context.Background())
				messages <- doneMsg{output: output.String()}
				return
			case protoevent.EventTypeError:
				var payload protoevent.ErrorEventPayload
				_ = json.Unmarshal(event.Payload, &payload)
				message := strings.TrimSpace(payload.Message)
				if message == "" {
					message = "runtime reported an unspecified error"
				}
				err := fmt.Errorf("%s", message)
				handle.Complete(runtimeRun.Error, output.String(), err)
				messages <- doneMsg{err: err}
				return
			case protoevent.EventTypeTurnInterrupted, protoevent.EventTypeCompactInterrupted:
				handle.Complete(runtimeRun.Interrupted, output.String(), nil)
				messages <- doneMsg{output: output.String()}
				return
			default:
				messages <- timelineMsg{event: event}
			}
		}
	}
}

func assistantText(raw json.RawMessage) (text string) {
	var payload protoevent.MessageEventPayload
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	for _, part := range payload.Parts {
		if part.Type == protoinput.MessagePartTypeText {
			text += part.Text
		}
	}
	return text
}

func requestApprovalResume(ctx context.Context, runtime clientruntime.InteractiveRuntime, stream *clientruntime.TurnStream, event timeline.Event, messages chan<- tea.Msg, results chan<- resumeResult) {
	var payload protoevent.ApprovalRequiredEventPayload
	if json.Unmarshal(event.Payload, &payload) != nil {
		return
	}
	args := ""
	if payload.ArgumentsJSON != nil {
		args = *payload.ArgumentsJSON
	}
	reply := make(chan bool, 1)
	messages <- approvalRequest{toolName: payload.ToolName, args: args, reply: reply}
	go func() {
		var approved bool
		select {
		case <-ctx.Done():
			return
		case approved = <-reply:
		}
		err := runtime.Resume(context.Background(), stream.Ref, protoinput.ResumeTurnPayload{
			TurnID: event.TurnID, CheckpointID: payload.CheckpointID, InterruptID: payload.InterruptID,
			ToolName: payload.ToolName, ArgumentsInJSON: args,
			Approval: &protoinput.ApprovalDecision{Approved: approved},
		})
		select {
		case <-ctx.Done():
			return
		case results <- resumeResult{err: err}:
		}
	}()
}

func waitForStreamMsg(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		value, ok := <-ch
		if !ok {
			return nil
		}
		return value
	}
}
