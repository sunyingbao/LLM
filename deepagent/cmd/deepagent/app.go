package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/worker"
	inprocess "eino-cli/deepagent/worker/inprocess"
	tea "github.com/charmbracelet/bubbletea"
)

type App struct {
	deps *appDeps
}

func NewApp(ctx context.Context, cfg AppConfig) (*App, error) {
	deps, err := buildDeps(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &App{deps: deps}, nil
}

func (a *App) Run(ctx context.Context, args []string) int {
	defer func() { _ = a.deps.worker.Close(context.Background()) }()

	prompt, err := a.deps.config.oneShotPrompt(args, os.Stdin)
	if err != nil {
		fmt.Printf("解析单次运行输入失败: %v\n", err)
		return 1
	}
	if strings.TrimSpace(prompt) != "" {
		return a.runOneShot(ctx, prompt)
	}
	return a.runInteractive(ctx)
}

func (a *App) runOneShot(ctx context.Context, prompt string) int {
	thread, err := a.resolveOneShotThread(ctx, prompt)
	if err != nil {
		fmt.Printf("创建/恢复 thread 失败: %v\n", err)
		return 1
	}
	sub, err := a.deps.worker.SubscribeSessionEvents(ctx, thread.SessionID, 128)
	if err != nil {
		fmt.Printf("订阅事件失败: %v\n", err)
		return 1
	}
	defer sub.Close()

	if _, err := a.deps.service.SendUserMessage(ctx, thread.ID, prompt); err != nil {
		fmt.Printf("发送消息失败: %v\n", err)
		return 1
	}
	if err := streamOneShot(ctx, thread.ID, sub.Events); err != nil {
		fmt.Printf("\n执行失败: %v\n", err)
		return 1
	}
	fmt.Println()
	return 0
}

func (a *App) resolveOneShotThread(ctx context.Context, prompt string) (*inprocess.ThreadState, error) {
	if strings.TrimSpace(a.deps.config.ResumeThreadID) != "" {
		binding, err := a.deps.service.BindThread(ctx, a.deps.config.ResumeThreadID)
		if err != nil {
			return nil, err
		}
		return binding.Thread, nil
	}
	return a.deps.service.CreateRootThread(ctx, prompt)
}

func streamOneShot(ctx context.Context, rootThreadID string, events <-chan *agentworker.Event) error {
	seenAnswer := false
	seenReasoning := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			if ev == nil {
				continue
			}
			isRootEvent := ev.ThreadID == rootThreadID
			if !isRootEvent {
				continue
			}
			payload := decodeLocalEventPayload(ev.Payload)
			switch ev.Type {
			case agentworker.EventType(agentthread.EventLLMToken):
				if payload.ReasoningText != "" {
					if !seenReasoning {
						fmt.Print("思考: ")
						seenReasoning = true
					}
					fmt.Print(payload.ReasoningText)
				}
				if payload.Text != "" && seenReasoning && !seenAnswer {
					fmt.Print("\n")
				}
				if payload.Text != "" {
					seenAnswer = true
					fmt.Print(payload.Text)
				}
			case agentworker.EventType(agentthread.EventLLMEnd):
				if !seenAnswer && payload.Message != "" && payload.Text == "" {
					if payload.ReasoningText != "" {
						fmt.Printf("思考: %s\n", payload.ReasoningText)
					}
					fmt.Print(payload.Message)
				}
			case agentworker.EventType(agentthread.EventToolStart):
				fmt.Printf("\n[tool] %s\n", payload.Name)
			case agentworker.EventType(agentthread.EventApproveRequested):
				return errors.New("当前 one-shot 模式不支持审批恢复，请使用交互模式")
			case agentworker.EventType(agentthread.EventError):
				return errors.New(payload.Message)
			case agentworker.EventType(agentthread.EventTurnEnd):
				return nil
			}
		}
	}
}

func (a *App) runInteractive(ctx context.Context) int {
	model := newChatModel(ctx, a.deps.service)
	if target := strings.TrimSpace(a.deps.config.ResumeThreadID); target != "" {
		model.initialThreadID = target
	} else {
		model.autoResume = a.deps.config.AutoResume
	}
	if _, err := tea.NewProgram(model).Run(); err != nil {
		fmt.Printf("运行 TUI 失败: %v\n", err)
		return 1
	}
	return 0
}
