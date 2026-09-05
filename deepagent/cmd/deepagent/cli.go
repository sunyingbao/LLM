package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"eino-cli/deepagent/backend/cli/tui"
	backendconfig "eino-cli/deepagent/backend/config"
	"eino-cli/deepagent/backend/sandbox"
	"eino-cli/deepagent/backend/sandbox/aio"
	"eino-cli/deepagent/backend/sandbox/local"
	"eino-cli/deepagent/backend/session"
	protoevent "eino-cli/deepagent/cloud/protocol/event"
	clientruntime "eino-cli/deepagent/host/runtime"
	sdkruntime "eino-cli/deepagent/runtime"
)

func runCLI(
	ctx context.Context,
	cfg *backendconfig.Config,
	opts CLIOptions,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
) (err error) {
	prompt, err := opts.oneShotPrompt(args, stdin)
	if err != nil {
		return err
	}
	runtimeKind, err := clientruntime.RuntimeKindFromEnv()
	if err != nil {
		return err
	}
	if runtimeKind == sdkruntime.RuntimeRemote {
		return runRemoteCLI(ctx, opts, prompt, stdout)
	}
	return runLocalCLI(ctx, cfg, opts, prompt, stdout)
}

func runLocalCLI(ctx context.Context, cfg *backendconfig.Config, opts CLIOptions, prompt string, stdout io.Writer) (err error) {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	if strings.TrimSpace(opts.ResumeSessionID) != "" {
		return fmt.Errorf("--resume_session_id is only available in remote runtime")
	}
	workDir, err := filepath.Abs(opts.WorkDir)
	if err != nil {
		return fmt.Errorf("prepare workdir: %w", err)
	}
	if err = os.Chdir(workDir); err != nil {
		return fmt.Errorf("enter workdir: %w", err)
	}
	sessionID, err := session.StartSession(ctx)
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	sandboxManager, err := buildSandboxManager(cfg, sessionID)
	if err != nil {
		return fmt.Errorf("build sandbox manager: %w", err)
	}
	sandbox.SetDefault(sandboxManager)
	defer sandbox.ShutdownDefault()
	if err = resetAgentMessagesLog(); err != nil {
		return fmt.Errorf("reset agent messages log: %w", err)
	}
	runtime, err := clientruntime.NewInteractiveRuntime(ctx, cfg, sessionID)
	if err != nil {
		return fmt.Errorf("build runtime: %w", err)
	}
	localRuntime, ok := runtime.(*clientruntime.LocalRuntime)
	if !ok {
		return fmt.Errorf("local runtime does not support local threads")
	}
	if threadID := strings.TrimSpace(opts.ResumeThreadID); threadID != "" {
		if err = localRuntime.OpenThread(ctx, threadID); err != nil {
			return fmt.Errorf("open thread: %w", err)
		}
	} else if opts.AutoResume {
		if _, err = localRuntime.OpenLatestThread(ctx); err != nil {
			return fmt.Errorf("open latest thread: %w", err)
		}
	}
	if strings.TrimSpace(prompt) != "" {
		return runOneShot(ctx, runtime, prompt, stdout)
	}
	err = tui.Run(runtime, sessionID, cfg)
	return err
}

func runRemoteCLI(ctx context.Context, opts CLIOptions, prompt string, stdout io.Writer) (err error) {
	if strings.TrimSpace(opts.ResumeThreadID) != "" {
		return fmt.Errorf("--resume_thread_id is local-only; use --resume_session_id in remote runtime")
	}
	runtime, err := clientruntime.NewInteractiveRuntime(ctx, nil, "")
	if err != nil {
		return fmt.Errorf("build runtime: %w", err)
	}
	httpRuntime, ok := runtime.(*clientruntime.HTTPRuntime)
	if !ok {
		return fmt.Errorf("remote runtime does not support backend sessions")
	}
	if sessionID := strings.TrimSpace(opts.ResumeSessionID); sessionID != "" {
		if err = httpRuntime.OpenSession(ctx, sessionID); err != nil {
			return fmt.Errorf("open session: %w", err)
		}
	} else if opts.AutoResume {
		if _, err = httpRuntime.OpenLatestSession(ctx); err != nil {
			return fmt.Errorf("open latest session: %w", err)
		}
	}
	if strings.TrimSpace(prompt) != "" {
		return runOneShot(ctx, runtime, prompt, stdout)
	}
	err = tui.Run(runtime, "")
	return err
}

func runOneShot(ctx context.Context, runtime clientruntime.InteractiveRuntime, prompt string, stdout io.Writer) (err error) {
	stream, err := runtime.StartTurn(ctx, prompt)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	printedText := make(map[string]string)
	messageKey := func(responseID string, messageID *string) (key string) {
		if responseID != "" {
			return "response:" + responseID
		}
		if messageID != nil && *messageID != "" {
			return "message:" + *messageID
		}
		return ""
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-stream.Events:
			if !ok {
				if err = stream.Err(); err != nil {
					return err
				}
				return fmt.Errorf("timeline closed before turn completed")
			}
			if !stream.AcceptEvent(event) {
				continue
			}
			switch protoevent.EventType(event.EventType) {
			case protoevent.EventTypeAssistantDelta:
				var payload protoevent.AssistantDeltaEventPayload
				if err = json.Unmarshal(event.Payload, &payload); err != nil {
					return err
				}
				if payload.ThinkingContentDelta != "" {
					_, _ = fmt.Fprint(stdout, payload.ThinkingContentDelta)
				}
				if payload.Delta != "" {
					printedText[messageKey(payload.LLMResponseID, payload.MessageID)] += payload.Delta
					_, _ = fmt.Fprint(stdout, payload.Delta)
				}
			case protoevent.EventTypeAssistantMessage:
				var payload protoevent.MessageEventPayload
				if err = json.Unmarshal(event.Payload, &payload); err != nil {
					return err
				}
				key := messageKey(payload.LLMResponseID, payload.MessageID)
				text := assistantText(event.Payload)
				previous := printedText[key]
				if strings.HasPrefix(text, previous) {
					_, _ = fmt.Fprint(stdout, text[len(previous):])
				} else if text != "" {
					_, _ = fmt.Fprintf(stdout, "\n[recovered message]\n%s", text)
				}
				printedText[key] = text
				if key == "" {
					delete(printedText, key)
				}
			case protoevent.EventTypeToolCallStarted:
				var payload protoevent.ToolCallEventPayload
				if err = json.Unmarshal(event.Payload, &payload); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(stdout, "\n[tool] %s\n", payload.ToolName)
			case protoevent.EventTypeApprovalRequired, protoevent.EventTypePlanInputRequired, protoevent.EventTypeInterruptRequired:
				return errors.New("one-shot mode cannot answer interactive requests")
			case protoevent.EventTypeTurnFinished:
				_, _ = fmt.Fprintln(stdout)
				return nil
			case protoevent.EventTypeError:
				var payload protoevent.ErrorEventPayload
				_ = json.Unmarshal(event.Payload, &payload)
				return errors.New(strings.TrimSpace(payload.Message))
			case protoevent.EventTypeTurnInterrupted, protoevent.EventTypeCompactInterrupted:
				return errors.New("turn interrupted")
			}
		}
	}
}

func buildSandboxManager(cfg *backendconfig.Config, sessionID string) (manager sandbox.SandboxManager, err error) {
	use := ""
	if cfg != nil {
		use = strings.TrimSpace(cfg.Sandbox.Use)
	}
	switch use {
	case "", "local":
		manager, err = local.New(sessionID)
		return manager, err
	case "aio":
		manager, err = aio.New(cfg, sessionID)
		return manager, err
	default:
		return nil, fmt.Errorf("sandbox: unknown sandbox.use %q (allowed: local, aio)", use)
	}
}

func resetAgentMessagesLog() (err error) {
	path := backendconfig.AgentMessagesLogPath()
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	err = os.WriteFile(path, nil, 0o644)
	return err
}

func assistantText(raw json.RawMessage) (text string) {
	var payload protoevent.MessageEventPayload
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	for _, part := range payload.Parts {
		if part.Type == protoevent.MessagePartTypeText {
			text += part.Text
		}
	}
	return text
}
