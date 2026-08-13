package planmode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"eino-cli/deepagent/core/middleware"
)

const (
	MiddlewareName       = "plan_mode"
	ToolRequestUserInput = "request_user_input"
)

const DefaultPrompt = `# Plan Mode

You are in Plan Mode. Your job is to chat your way to a decision-complete plan, not to implement it. The final plan should be detailed enough for another engineer or agent to execute without making new product or technical decisions.

## Execution vs. planning
- You may use non-mutating exploration to understand the environment, such as reading files, searching code, inspecting configs, or running checks that do not intentionally change project state.
- Do not perform implementation work, edit files, run codegen, apply patches, or intentionally mutate project state.
- If the host exposes mutating tools, avoid using them in Plan Mode. Tool safety is controlled by the host application, not this middleware.

## Phase 1: ground in the environment
- Explore first and ask second. Resolve unknowns that can be answered from the repo, runtime context, configs, schemas, types, or docs.
- Do not ask the user questions that can be answered by reasonable non-mutating exploration.
- Ask only when the remaining ambiguity is about intent, preference, tradeoff, or missing context that cannot be discovered.

## Phase 2: clarify intent
- Keep asking until you can state the goal, success criteria, audience, in/out of scope, constraints, current state, and key preferences.
- If a high-impact ambiguity remains, ask before producing the final plan.

## Phase 3: converge on implementation
- Make the plan decision-complete: approach, important APIs or interfaces, data flow, edge cases, tests, and assumptions.
- Separate discoverable facts from preferences. Discoverable facts should be investigated first; preferences and tradeoffs should be asked early.

## Asking questions
- Prefer request_user_input for important questions.
- Use it only for decisions that materially change the plan, confirm important assumptions, or choose between meaningful tradeoffs.
- Ask one to three short questions at a time.
- Provide meaningful mutually exclusive options, put the recommended option first, and avoid filler options.

## Finalization
- Only output the final plan when it leaves no decisions to the implementer.
- Put the final plan inside exactly one <proposed_plan> block.
- Do not ask whether to proceed inside the final plan.`

const defaultPromptWithoutAskUser = `# Plan Mode

You are in Plan Mode. Your job is to chat your way to a decision-complete plan, not to implement it. The final plan should be detailed enough for another engineer or agent to execute without making new product or technical decisions.

## Execution vs. planning
- You may use non-mutating exploration to understand the environment, such as reading files, searching code, inspecting configs, or running checks that do not intentionally change project state.
- Do not perform implementation work, edit files, run codegen, apply patches, or intentionally mutate project state.
- If the host exposes mutating tools, avoid using them in Plan Mode. Tool safety is controlled by the host application, not this middleware.

## Phase 1: ground in the environment
- Explore first and ask second. Resolve unknowns that can be answered from the repo, runtime context, configs, schemas, types, or docs.
- Do not ask the user questions that can be answered by reasonable non-mutating exploration.
- Ask only when the remaining ambiguity is about intent, preference, tradeoff, or missing context that cannot be discovered.

## Phase 2: clarify intent
- Keep asking until you can state the goal, success criteria, audience, in/out of scope, constraints, current state, and key preferences.
- If a high-impact ambiguity remains, ask before producing the final plan.

## Phase 3: converge on implementation
- Make the plan decision-complete: approach, important APIs or interfaces, data flow, edge cases, tests, and assumptions.
- Separate discoverable facts from preferences. Discoverable facts should be investigated first; preferences and tradeoffs should be asked early.

## Asking questions
- Ask directly only for decisions that materially change the plan, confirm important assumptions, or choose between meaningful tradeoffs.
- Ask one to three short questions at a time.
- Provide meaningful mutually exclusive options, put the recommended option first, and avoid filler options.

## Finalization
- Only output the final plan when it leaves no decisions to the implementer.
- Put the final plan inside exactly one <proposed_plan> block.
- Do not ask whether to proceed inside the final plan.`

type Config struct {
	// EnableAskUser controls whether request_user_input is exposed.
	// A nil Config enables it by default; an explicit zero-value Config disables it.
	EnableAskUser bool

	// Prompt overrides DefaultPrompt when non-empty.
	Prompt string
}

type PlanModeMiddleware struct {
	middleware.BaseMiddleware
	prompt        string
	enableAskUser bool
}

func New(cfg *Config) *PlanModeMiddleware {
	enableAskUser := true
	prompt := DefaultPrompt
	if cfg != nil {
		enableAskUser = cfg.EnableAskUser
		if !enableAskUser {
			prompt = defaultPromptWithoutAskUser
		}
		if strings.TrimSpace(cfg.Prompt) != "" {
			prompt = cfg.Prompt
		}
	}
	return &PlanModeMiddleware{
		prompt:        prompt,
		enableAskUser: enableAskUser,
	}
}

func (m *PlanModeMiddleware) Name() string {
	return MiddlewareName
}

func (m *PlanModeMiddleware) Tools(ctx context.Context) ([]tool.BaseTool, error) {
	if !m.enableAskUser {
		return nil, nil
	}
	return []tool.BaseTool{newRequestUserInputTool()}, nil
}

func (m *PlanModeMiddleware) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
	if strings.TrimSpace(m.prompt) == "" {
		return nil, nil
	}
	return []*schema.Message{schema.SystemMessage(m.prompt)}, nil
}

type QuestionOption struct {
	Label       string `json:"label" jsonschema:"description=User-facing label for this option"`
	Description string `json:"description" jsonschema:"description=Short explanation of the impact or tradeoff"`
}

type Question struct {
	ID       string           `json:"id" jsonschema:"description=Stable snake_case identifier used to map the answer"`
	Header   string           `json:"header" jsonschema:"description=Short UI header for this question"`
	Question string           `json:"question" jsonschema:"description=Single-sentence question shown to the user"`
	Options  []QuestionOption `json:"options" jsonschema:"description=Mutually exclusive options; put the recommended option first"`
}

type RequestUserInput struct {
	Questions []Question `json:"questions" jsonschema:"description=Questions to show the user; prefer one and do not exceed three"`
}

type RequestUserInputAnswer struct {
	Answers []string `json:"answers"`
}

type RequestUserInputResponse struct {
	Answers map[string]RequestUserInputAnswer `json:"answers"`
}

type RequestUserInputInfo struct {
	Questions []Question `json:"questions"`
}

func (info *RequestUserInputInfo) String() string {
	if info == nil || len(info.Questions) == 0 {
		return "request_user_input is waiting for user input"
	}
	var sb strings.Builder
	sb.WriteString("request_user_input is waiting for user input:\n")
	for i, q := range info.Questions {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, q.Question))
	}
	return strings.TrimRight(sb.String(), "\n")
}

type requestUserInputState struct {
	Questions []Question `json:"questions"`
}

func init() {
	schema.Register[*RequestUserInputInfo]()
	schema.Register[*requestUserInputState]()
	schema.Register[*RequestUserInputResponse]()
}

func askUser(ctx context.Context, input *RequestUserInput) (string, error) {
	wasInterrupted, _, storedState := tool.GetInterruptState[*requestUserInputState](ctx)
	if !wasInterrupted {
		if err := validateRequest(input); err != nil {
			return "", err
		}
		info := &RequestUserInputInfo{Questions: cloneQuestions(input.Questions)}
		state := &requestUserInputState{Questions: cloneQuestions(input.Questions)}
		return "", tool.StatefulInterrupt(ctx, info, state)
	}

	isResumeTarget, hasData, data := tool.GetResumeContext[*RequestUserInputResponse](ctx)
	if !isResumeTarget {
		info := &RequestUserInputInfo{Questions: cloneQuestions(storedState.Questions)}
		return "", tool.StatefulInterrupt(ctx, info, storedState)
	}
	if !hasData || data == nil {
		return "", fmt.Errorf("request_user_input resumed without answers")
	}
	out, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshal request_user_input response: %w", err)
	}
	return string(out), nil
}

func newRequestUserInputTool() tool.InvokableTool {
	t, err := utils.InferTool(
		ToolRequestUserInput,
		"Request structured user input for planning decisions and wait for the response.",
		askUser,
	)
	if err != nil {
		panic(err)
	}
	return t
}

func validateRequest(input *RequestUserInput) error {
	if input == nil || len(input.Questions) == 0 {
		return fmt.Errorf("request_user_input requires at least one question")
	}
	if len(input.Questions) > 3 {
		return fmt.Errorf("request_user_input supports at most three questions")
	}
	seenIDs := map[string]struct{}{}
	for i, q := range input.Questions {
		if strings.TrimSpace(q.ID) == "" {
			return fmt.Errorf("questions[%d].id is required", i)
		}
		if _, ok := seenIDs[q.ID]; ok {
			return fmt.Errorf("questions[%d].id is duplicated", i)
		}
		seenIDs[q.ID] = struct{}{}
		if strings.TrimSpace(q.Header) == "" {
			return fmt.Errorf("questions[%d].header is required", i)
		}
		if strings.TrimSpace(q.Question) == "" {
			return fmt.Errorf("questions[%d].question is required", i)
		}
		if len(q.Options) == 0 {
			return fmt.Errorf("questions[%d].options is required", i)
		}
		for j, opt := range q.Options {
			if strings.TrimSpace(opt.Label) == "" {
				return fmt.Errorf("questions[%d].options[%d].label is required", i, j)
			}
			if strings.TrimSpace(opt.Description) == "" {
				return fmt.Errorf("questions[%d].options[%d].description is required", i, j)
			}
		}
	}
	return nil
}

func cloneQuestions(in []Question) []Question {
	if len(in) == 0 {
		return nil
	}
	out := make([]Question, len(in))
	for i, q := range in {
		out[i] = q
		if len(q.Options) > 0 {
			out[i].Options = append([]QuestionOption(nil), q.Options...)
		}
	}
	return out
}
