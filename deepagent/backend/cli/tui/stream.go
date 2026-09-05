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
	sdkruntime "eino-cli/deepagent/runtime"
	tea "github.com/charmbracelet/bubbletea"

	runtimecontext "eino-cli/deepagent/host/executioncontext"
	runtimeRun "eino-cli/deepagent/host/run"
)

type chunkMsg string
type streamOutputMsg string
type timelineMsg struct{ event timeline.Event }

type doneMsg struct {
	output string
	err    error
}

type resumeResult struct {
	err error
}

type streamStartedMsg struct {
	messages <-chan tea.Msg
	cancel   context.CancelFunc
	detach   func()
}

type stopResultMsg struct {
	err error
}

type streamRun struct {
	ctx      context.Context
	cancel   context.CancelFunc
	complete func(status runtimeRun.Status, output string, err error)
	snapshot func()
}

func startStreamCmd(runtime clientruntime.InteractiveRuntime, sessionID, prompt string, runManager *runtimeRun.Manager) (cmd tea.Cmd) {
	cmd = func() tea.Msg {
		messages, cancel, detach := startStream(runtime, sessionID, prompt, runManager)
		return streamStartedMsg{messages: messages, cancel: cancel, detach: detach}
	}
	return cmd
}

func startStream(runtime clientruntime.InteractiveRuntime, sessionID, prompt string, runManager *runtimeRun.Manager) (messages <-chan tea.Msg, cancel context.CancelFunc, detach func()) {
	streamMessages := make(chan tea.Msg, 64)
	var activeRun streamRun
	if runManager != nil {
		ctx := runtimecontext.WithSessionID(context.Background(), sessionID)
		handle, err := runManager.Begin(ctx, prompt)
		if err != nil {
			streamMessages <- doneMsg{err: err}
			close(streamMessages)
			return streamMessages, func() {}, func() {}
		}
		activeRun = streamRun{
			ctx: handle.Context, cancel: handle.Cancel,
			complete: func(status runtimeRun.Status, output string, completeErr error) {
				handle.Complete(status, output, completeErr)
			},
			snapshot: func() { _ = handle.SaveWorkspaceSnapshot(context.Background()) },
		}
	} else {
		streamCtx, streamCancel := context.WithCancel(context.Background())
		activeRun = streamRun{ctx: streamCtx, cancel: streamCancel, complete: func(runtimeRun.Status, string, error) {}, snapshot: func() {}}
	}
	turnStream, err := runtime.StartTurn(activeRun.ctx, prompt)
	if err != nil {
		activeRun.complete(runtimeRun.Error, "", err)
		streamMessages <- doneMsg{err: err}
		close(streamMessages)
		return streamMessages, activeRun.cancel, func() {}
	}
	stopResults := make(chan error, 1)
	var stopMu sync.Mutex
	stopPending := false
	cancel = func() {
		if runtime.RuntimeKind() == sdkruntime.RuntimeRemote {
			stopMu.Lock()
			if stopPending {
				stopMu.Unlock()
				return
			}
			stopPending = true
			stopMu.Unlock()
			go func() {
				stopErr := turnStream.Stop(context.Background())
				stopMu.Lock()
				stopPending = false
				stopMu.Unlock()
				select {
				case <-activeRun.ctx.Done():
				case stopResults <- stopErr:
				}
			}()
			return
		}
		stopMu.Lock()
		if !stopPending {
			stopPending = true
			activeRun.cancel()
			_ = turnStream.Stop(context.Background())
			_ = turnStream.Close()
		}
		stopMu.Unlock()
	}
	detach = func() {
		activeRun.cancel()
		_ = turnStream.Close()
	}
	go consumeTimeline(runtime, activeRun, turnStream, streamMessages, stopResults)
	return streamMessages, cancel, detach
}

func followStream(runtime clientruntime.InteractiveRuntime, ctx context.Context, followCancel context.CancelFunc, stream *clientruntime.TurnStream) (started streamStartedMsg) {
	streamMessages := make(chan tea.Msg, 64)
	activeRun := streamRun{ctx: ctx, cancel: followCancel, complete: func(runtimeRun.Status, string, error) {}, snapshot: func() {}}
	stopResults := make(chan error, 1)
	var stopMu sync.Mutex
	stopPending := false
	started.messages = streamMessages
	started.cancel = func() {
		stopMu.Lock()
		if stopPending {
			stopMu.Unlock()
			return
		}
		stopPending = true
		stopMu.Unlock()
		go func() {
			stopErr := stream.Stop(context.Background())
			stopMu.Lock()
			stopPending = false
			stopMu.Unlock()
			select {
			case <-ctx.Done():
			case stopResults <- stopErr:
			}
		}()
	}
	started.detach = func() {
		followCancel()
		_ = stream.Close()
	}
	go consumeTimeline(runtime, activeRun, stream, streamMessages, stopResults)
	return started
}

func consumeTimeline(runtime clientruntime.InteractiveRuntime, activeRun streamRun, stream *clientruntime.TurnStream, messages chan<- tea.Msg, stopResults <-chan error) {
	defer close(messages)
	defer func() { _ = stream.Close() }()
	defer activeRun.cancel()
	var output strings.Builder
	var parts []string
	indexes := make(map[string]int)
	anonymousIndex := 0
	messageIndex := func(responseID string, messageID *string) (index int) {
		key := "response:" + responseID
		if responseID == "" {
			key = fmt.Sprintf("anonymous:%d", anonymousIndex)
			if messageID != nil && *messageID != "" {
				key = "message:" + *messageID
			}
		}
		if index, ok := indexes[key]; ok {
			return index
		}
		index = len(parts)
		indexes[key] = index
		parts = append(parts, "")
		return index
	}
	send := func(message tea.Msg) (sent bool) {
		return sendStreamMessage(activeRun.ctx, messages, message)
	}
	finish := func(status runtimeRun.Status, err error) {
		activeRun.complete(status, output.String(), err)
		if status == runtimeRun.Success {
			activeRun.snapshot()
		}
		send(doneMsg{output: output.String(), err: err})
	}
	resumeResults := make(chan resumeResult, 1)
	for {
		select {
		case <-activeRun.ctx.Done():
			finish(runtimeRun.Interrupted, activeRun.ctx.Err())
			return
		case result := <-resumeResults:
			if result.err != nil {
				finish(runtimeRun.Error, result.err)
				return
			}
		case stopErr := <-stopResults:
			if !send(stopResultMsg{err: stopErr}) {
				return
			}
		case event, ok := <-stream.Events:
			if !ok {
				err := stream.Err()
				if err == nil {
					err = fmt.Errorf("timeline closed before turn completed")
				}
				finish(runtimeRun.Error, err)
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
					finish(runtimeRun.Error, err)
					return
				}
				parts[messageIndex(payload.LLMResponseID, payload.MessageID)] += payload.Delta
				output.WriteString(payload.Delta)
				if !send(chunkMsg(payload.Delta)) {
					return
				}
			case protoevent.EventTypeAssistantMessage:
				var payload protoevent.MessageEventPayload
				if err := json.Unmarshal(event.Payload, &payload); err != nil {
					finish(runtimeRun.Error, err)
					return
				}
				parts[messageIndex(payload.LLMResponseID, payload.MessageID)] = assistantText(event.Payload)
				output.Reset()
				for _, part := range parts {
					output.WriteString(part)
				}
				if payload.LLMResponseID == "" && (payload.MessageID == nil || *payload.MessageID == "") {
					anonymousIndex++
				}
				if !send(streamOutputMsg(output.String())) || !send(timelineMsg{event: event}) {
					return
				}
			case protoevent.EventTypeApprovalRequired:
				if !send(timelineMsg{event: event}) {
					return
				}
				requestApprovalResume(activeRun.ctx, runtime, stream, event, messages, resumeResults)
			case protoevent.EventTypePlanInputRequired:
				if !send(timelineMsg{event: event}) {
					return
				}
				requestPlanInputResume(activeRun.ctx, runtime, stream, event, messages, resumeResults)
			case protoevent.EventTypeInterruptRequired:
				if !send(timelineMsg{event: event}) {
					return
				}
				requestInterruptResume(activeRun.ctx, runtime, stream, event, messages, resumeResults)
			case protoevent.EventTypeTurnFinished:
				send(timelineMsg{event: event})
				finish(runtimeRun.Success, nil)
				return
			case protoevent.EventTypeError:
				var payload protoevent.ErrorEventPayload
				_ = json.Unmarshal(event.Payload, &payload)
				message := strings.TrimSpace(payload.Message)
				if message == "" {
					message = "runtime reported an unspecified error"
				}
				err := fmt.Errorf("%s", message)
				finish(runtimeRun.Error, err)
				return
			case protoevent.EventTypeTurnInterrupted, protoevent.EventTypeCompactInterrupted:
				send(timelineMsg{event: event})
				finish(runtimeRun.Interrupted, nil)
				return
			default:
				if !send(timelineMsg{event: event}) {
					return
				}
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
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		sendResumeResult(ctx, results, fmt.Errorf("decode approval request: %w", err))
		return
	}
	args := ""
	if payload.ArgumentsJSON != nil {
		args = *payload.ArgumentsJSON
	}
	reply := make(chan bool, 1)
	if !sendStreamMessage(ctx, messages, approvalRequest{toolName: payload.ToolName, args: args, reply: reply}) {
		return
	}
	go func() {
		var approved bool
		select {
		case <-ctx.Done():
			return
		case approved = <-reply:
		}
		err := runtime.Resume(ctx, stream.Ref, protoinput.ResumeTurnPayload{
			TurnID: event.TurnID, CheckpointID: payload.CheckpointID, InterruptID: payload.InterruptID,
			ToolName: payload.ToolName, ArgumentsInJSON: args, ConsumedMessageIDs: append([]string(nil), payload.ConsumedMessageIDs...),
			Approval: &protoinput.ApprovalDecision{Approved: approved},
		})
		sendResumeResult(ctx, results, err)
	}()
}

func requestPlanInputResume(ctx context.Context, runtime clientruntime.InteractiveRuntime, stream *clientruntime.TurnStream, event timeline.Event, messages chan<- tea.Msg, results chan<- resumeResult) {
	var payload protoevent.PlanInputRequiredEventPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		sendResumeResult(ctx, results, fmt.Errorf("decode plan input request: %w", err))
		return
	}
	questions := make([]questionPrompt, 0, len(payload.Questions))
	for _, question := range payload.Questions {
		if question == nil || strings.TrimSpace(question.ID) == "" || strings.TrimSpace(question.Question) == "" {
			sendResumeResult(ctx, results, fmt.Errorf("plan input request contains an invalid question"))
			return
		}
		options := make([]string, 0, len(question.Options))
		for _, option := range question.Options {
			if option != nil {
				options = append(options, option.Label)
			}
		}
		questions = append(questions, questionPrompt{id: question.ID, header: question.Header, question: question.Question, options: options})
	}
	if len(questions) == 0 {
		sendResumeResult(ctx, results, fmt.Errorf("plan input request contains no questions"))
		return
	}
	reply := make(chan map[string][]string, 1)
	if !sendStreamMessage(ctx, messages, questionRequest{questions: questions, answers: make(map[string][]string), reply: reply}) {
		return
	}
	go func() {
		var answers map[string][]string
		select {
		case <-ctx.Done():
			return
		case answers = <-reply:
		}
		response := &protoinput.RequestUserInputResponse{Answers: make(map[string]protoinput.RequestUserInputAnswer, len(answers))}
		for id, values := range answers {
			response.Answers[id] = protoinput.RequestUserInputAnswer{Answers: append([]string(nil), values...)}
		}
		err := runtime.Resume(ctx, stream.Ref, protoinput.ResumeTurnPayload{
			TurnID: event.TurnID, CheckpointID: payload.CheckpointID, InterruptID: payload.InterruptID,
			ConsumedMessageIDs: append([]string(nil), payload.ConsumedMessageIDs...), RequestUserInput: response,
		})
		sendResumeResult(ctx, results, err)
	}()
}

func requestInterruptResume(ctx context.Context, runtime clientruntime.InteractiveRuntime, stream *clientruntime.TurnStream, event timeline.Event, messages chan<- tea.Msg, results chan<- resumeResult) {
	var payload protoevent.InterruptRequiredEventPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		sendResumeResult(ctx, results, fmt.Errorf("decode interrupt request: %w", err))
		return
	}
	if payload.Kind != "follow_up" {
		sendResumeResult(ctx, results, fmt.Errorf("unsupported interrupt request kind %q", payload.Kind))
		return
	}
	var info struct {
		Questions []string `json:"questions"`
	}
	if err := json.Unmarshal(payload.Info, &info); err != nil || len(info.Questions) == 0 {
		sendResumeResult(ctx, results, fmt.Errorf("follow_up interrupt contains no valid questions"))
		return
	}
	questionText := strings.Join(info.Questions, "\n")
	reply := make(chan map[string][]string, 1)
	request := questionRequest{questions: []questionPrompt{{id: "answer", header: "Follow-up", question: questionText}}, answers: make(map[string][]string), reply: reply}
	if !sendStreamMessage(ctx, messages, request) {
		return
	}
	go func() {
		var answers map[string][]string
		select {
		case <-ctx.Done():
			return
		case answers = <-reply:
		}
		answer := ""
		if values := answers["answer"]; len(values) > 0 {
			answer = values[0]
		}
		data, err := json.Marshal(map[string]string{"user_answer": answer})
		if err == nil {
			err = runtime.Resume(ctx, stream.Ref, protoinput.ResumeTurnPayload{
				TurnID: event.TurnID, CheckpointID: payload.CheckpointID, InterruptID: payload.InterruptID,
				ConsumedMessageIDs: append([]string(nil), payload.ConsumedMessageIDs...),
				Interrupt:          &protoinput.InterruptResumePayload{Kind: payload.Kind, InfoType: payload.InfoType, Data: data},
			})
		}
		sendResumeResult(ctx, results, err)
	}()
}

func sendStreamMessage(ctx context.Context, messages chan<- tea.Msg, message tea.Msg) (sent bool) {
	select {
	case <-ctx.Done():
		return false
	case messages <- message:
		return true
	}
}

func sendResumeResult(ctx context.Context, results chan<- resumeResult, err error) {
	select {
	case <-ctx.Done():
		return
	case results <- resumeResult{err: err}:
		return
	}
}

func waitForStreamMsg(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		value, ok := <-ch
		if !ok {
			return doneMsg{err: fmt.Errorf("timeline observer closed")}
		}
		return value
	}
}
