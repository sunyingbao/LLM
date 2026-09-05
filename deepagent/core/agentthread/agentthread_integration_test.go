package agentthread

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	deepagents "eino-cli/deepagent/core"
	"eino-cli/deepagent/core/checkpointer"
	deeptools "eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/core/types"
	"eino-cli/deepagent/mock/mock_model"

	"code.byted.org/gopkg/logs/v2"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/tidwall/gjson"
	"go.uber.org/mock/gomock"
)

// InMemoryHistoryRolloutStore 实现，用于测试
type InMemoryHistoryRolloutStore struct {
	mu   sync.RWMutex
	data map[string][]*HistoryRecord // threadID -> records
}

func NewInMemoryHistoryRolloutStore() *InMemoryHistoryRolloutStore {
	return &InMemoryHistoryRolloutStore{data: make(map[string][]*HistoryRecord)}
}

func (s *InMemoryHistoryRolloutStore) Append(_ context.Context, rec *HistoryRecord) error {
	if rec == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec.MessageID == 0 {
		rec.MessageID = int64(len(s.data[rec.ThreadID]) + 1)
	}
	if rec.Seq == 0 {
		rec.Seq = int64(len(s.data[rec.ThreadID]) + 1)
	}
	s.data[rec.ThreadID] = append(s.data[rec.ThreadID], rec)
	return nil
}

func (s *InMemoryHistoryRolloutStore) List(_ context.Context, q ListQuery) ([]*HistoryRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	records := s.data[q.ThreadID]
	if len(records) == 0 {
		return nil, nil
	}
	// 按 Seq 过滤与排序（源数据按 Append 顺序，与 Seq 单调一致）
	out := make([]*HistoryRecord, 0, len(records))
	switch q.Order {
	case ListOrderDESC:
		for i := len(records) - 1; i >= 0; i-- {
			rec := records[i]
			if rec == nil {
				continue
			}
			if q.TurnID != "" && rec.TurnID != q.TurnID {
				continue
			}
			if q.BeforeID != nil && !(rec.OrderSeq() < *q.BeforeID) {
				continue
			}
			out = append(out, rec)
			if q.Limit > 0 && len(out) >= q.Limit {
				break
			}
		}
	default: // ASC
		for i := 0; i < len(records); i++ {
			rec := records[i]
			if rec == nil {
				continue
			}
			if q.TurnID != "" && rec.TurnID != q.TurnID {
				continue
			}
			if q.AfterID != nil && !(rec.OrderSeq() > *q.AfterID) {
				continue
			}
			out = append(out, rec)
			if q.Limit > 0 && len(out) >= q.Limit {
				break
			}
		}
	}
	return out, nil
}

// 简单工具（可选）：计数器
type fakeToolCounter struct{ total int64 }

func (t *fakeToolCounter) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "counter",
		Desc: "incr counter",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"delta": {Desc: "number to add", Required: true, Type: schema.Integer},
		}),
	}, nil
}

func (t *fakeToolCounter) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	// 粗略解析：寻找最后一个数字
	var delta int64 = 1
	for i := len(argumentsInJSON) - 1; i >= 0; i-- {
		c := argumentsInJSON[i]
		if c >= '0' && c <= '9' {
			delta = int64(c - '0')
			break
		}
	}
	t.total += delta
	return fmt.Sprintf(`{"total": %d}`, t.total), nil
}

type countingToolResult struct {
	name     string
	result   string
	runCount int64
}

func (t *countingToolResult) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: "counting tool result"}, nil
}

func (t *countingToolResult) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	atomic.AddInt64(&t.runCount, 1)
	return t.result, nil
}

func (t *countingToolResult) RunCount() int64 {
	return atomic.LoadInt64(&t.runCount)
}

type testCompactionStrategy struct{}

func (s *testCompactionStrategy) ID() string {
	return "test_compaction"
}

func (s *testCompactionStrategy) Compact(ctx context.Context, current []*schema.Message) (*CompactionResult, error) {
	if len(current) == 0 {
		return nil, nil
	}
	summary := schema.AssistantMessage("summary", nil)
	return &CompactionResult{
		Compact: &CompactRecord{
			Summary:                summary,
			CompactStrategyID:      s.ID(),
			CompactStrategyPayload: "payload",
		},
		Rebuilt: []*schema.Message{summary},
	}, nil
}

func (s *testCompactionStrategy) Resume(ctx context.Context, compact *CompactRecord, postCompactMessages []*schema.Message) (*ResumeResult, error) {
	rebuilt := make([]*schema.Message, 0, len(postCompactMessages)+1)
	if compact != nil && compact.Summary != nil {
		rebuilt = append(rebuilt, compact.Summary)
	}
	rebuilt = append(rebuilt, postCompactMessages...)
	return &ResumeResult{Rebuilt: rebuilt}, nil
}

// 事件收集器
type eventCollector struct {
	mu     sync.Mutex
	events []Event
}

func (c *eventCollector) collect(ch <-chan Event) {
	for ev := range ch {
		c.mu.Lock()
		c.events = append(c.events, ev)
		c.mu.Unlock()
	}
}

func (c *eventCollector) waitFor(t testing.TB, typ EventType, d time.Duration) (Event, bool) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		for _, ev := range c.events {
			if ev.Type == typ {
				c.mu.Unlock()
				return ev, true
			}
		}
		c.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return Event{}, false
}

func (c *eventCollector) snapshot() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Event, len(c.events))
	copy(out, c.events)
	return out
}

func waitUntilEventfully(t testing.TB, timeout time.Duration, cond func() bool, format string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf(format, args...)
}

func hasEventType(events []Event, typ EventType) bool {
	for _, ev := range events {
		if ev.Type == typ {
			return true
		}
	}
	return false
}

func buildThreadForTest(threadID string, turnConfig *TurnConfig, hs HistoryRolloutStore, inputQueueSize int, strategy CompactionStrategy, eventBus chan Event) *DeepAgentThread {
	_ = inputQueueSize
	return New(threadID, turnConfig, eventBus, ThreadOptions{
		HistoryStore:       hs,
		CompactionStrategy: strategy,
	}, WithTurnIDProvider(func(ctx context.Context, threadID string, input *Message) string {
		if turnID, _ := ctx.Value(integrationTestTurnIDKey{}).(string); turnID != "" {
			return turnID
		}
		return defaultTurnIDProvider(ctx, threadID, input)
	}))
}

type integrationTestTurnIDKey struct{}

func runUserInput(ctx context.Context, th *DeepAgentThread, turnID, content string) error {
	result, err := th.SubmitInput(context.WithValue(ctx, integrationTestTurnIDKey{}, turnID), schema.UserMessage(content))
	if err != nil {
		return err
	}
	return result.TurnHandle.Wait(ctx)
}

func runUserInputAsync(ctx context.Context, th *DeepAgentThread, turnID, content string) <-chan error {
	errCh := make(chan error, 1)
	result, err := th.SubmitInput(context.WithValue(ctx, integrationTestTurnIDKey{}, turnID), schema.UserMessage(content))
	if err != nil {
		errCh <- err
		return errCh
	}
	go func() { errCh <- result.TurnHandle.Wait(ctx) }()
	return errCh
}

// 基础用户回合测试：验证事件与历史持久化
func TestAgentThread_UserInput_Basic(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)

	// 模型工具绑定
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	// 模型返回单条助手消息
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()
			sw.Send(schema.AssistantMessage("hello", nil), nil)
			return sr, nil
		},
	).AnyTimes()

	// 事件总线与历史存储
	bus := make(chan Event, 128)
	hs := NewInMemoryHistoryRolloutStore()

	th := buildThreadForTest("thread-basic", &TurnConfig{
		Agent: deepagents.Config{
			Model:           cm,
			Tools:           nil,
			CheckpointStore: checkpointer.NewInMemoryStore(),
		},
	}, hs, 8, nil, bus)
	if err := th.Init(ctx); err != nil {
		t.Fatalf("start err: %v", err)
	}

	// 启动事件采集
	coll := &eventCollector{}
	go coll.collect(bus)

	// 提交用户输入
	if err := runUserInput(ctx, th, "r1", "hi"); err != nil {
		t.Fatalf("submit err: %v", err)
	}

	// 等待 TurnEnd（LLMResponse 由回调触发，部分模型流式下不保证）
	if _, ok := coll.waitFor(t, EventTurnEnd, 2*time.Second); !ok {
		t.Fatalf("未收到 TurnEnd 事件")
	}

	// 校验历史：应至少包含用户与助手两条消息
	recs, err := hs.List(ctx, ListQuery{ThreadID: th.ThreadID, Order: ListOrderASC})
	if err != nil {
		t.Fatalf("history list err: %v", err)
	}
	if len(recs) < 2 {
		t.Fatalf("历史记录不足，got=%d", len(recs))
	}
	if recs[0].Message == nil || recs[0].Message.Role != schema.User {
		t.Fatalf("首条历史非用户消息")
	}
	if recs[len(recs)-1].Message == nil || recs[len(recs)-1].Message.Role != schema.Assistant {
		t.Fatalf("末条历史非助手消息")
	}
}

func TestAgentThread_CompactionPersistsCompactRecord(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()
			sw.Send(schema.AssistantMessage("hello-after-compact", nil), nil)
			return sr, nil
		},
	).AnyTimes()

	bus := make(chan Event, 128)
	hs := NewInMemoryHistoryRolloutStore()
	th := buildThreadForTest("thread-compact", &TurnConfig{
		Agent: deepagents.Config{
			Model:           cm,
			CheckpointStore: checkpointer.NewInMemoryStore(),
		},
	}, hs, 8, &testCompactionStrategy{}, bus)
	if err := th.Init(ctx); err != nil {
		t.Fatalf("start err: %v", err)
	}

	coll := &eventCollector{}
	go coll.collect(bus)

	if err := runUserInput(ctx, th, "r1", "trigger compact"); err != nil {
		t.Fatalf("submit err: %v", err)
	}

	if _, ok := coll.waitFor(t, EventTurnEnd, 2*time.Second); !ok {
		t.Fatalf("未收到 TurnEnd 事件")
	}
	if err := runUserInput(ctx, th, "r2", "next turn"); err != nil {
		t.Fatalf("second submit err: %v", err)
	}
	recs, err := hs.List(ctx, ListQuery{ThreadID: th.ThreadID, Order: ListOrderASC})
	if err != nil {
		t.Fatalf("history list err: %v", err)
	}
	var foundCompact bool
	for _, rec := range recs {
		if rec != nil && rec.Type == HistoryRecordCompact {
			foundCompact = true
			break
		}
	}
	if !foundCompact {
		t.Fatalf("expected compact record to be persisted, got=%+v", recs)
	}
}

type customState struct {
	Total   int64 `json:"total"`
	resumed int64
}

func (c *customState) MarshalRuntimeState() string {
	logs.Info("marshal runtime state: %s", fmt.Sprintf(`{"total": %d}`, c.Total))
	return fmt.Sprintf(`{"total": %d}`, c.Total)
}

func (c *customState) UnmarshalRuntimeState(data string) error {
	logs.Info("unmarshal runtime state: %s", data)
	c.Total = gjson.Get(data, "total").Int()
	c.resumed = c.Total
	return nil
}

// 中断审批与恢复测试
func TestAgentThread_ApproveAndResume(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cs := customState{
		Total: 100,
	}

	// 绑定工具
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	// 模型：第一次返回工具调用（counter），随后返回完成消息
	callSeq := 0
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()
			if callSeq == 0 {
				tc := schema.ToolCall{ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "counter", Arguments: `{"delta":2}`}}
				sw.Send(schema.AssistantMessage("call tool", []schema.ToolCall{tc}), nil)
			} else {
				// 第二次调用，应该是中断恢复了
				state := types.StateFromContext(ctx)
				if state == nil {
					t.Errorf("state should not be nil")
				} else if cs.resumed != 100 {
					t.Errorf("custom state resumed should be 100, got=%d", cs.resumed)
				}

				sw.Send(schema.AssistantMessage("done", nil), nil)
			}
			callSeq++
			return sr, nil
		},
	).AnyTimes()

	// 工具 + HITL 审批：首次必中断
	counter := &fakeToolCounter{}

	bus := make(chan Event, 256)
	hs := NewInMemoryHistoryRolloutStore()
	hitl := &deepagents.HITLConfig{ToolPolicyGates: map[string]deeptools.ToolPolicyGate{"counter": deeptools.ApprovalGate(func(context.Context, *deeptools.ApprovalInfo) bool { return true })}}

	th := buildThreadForTest("thread-approve", &TurnConfig{
		Agent: deepagents.Config{
			Model:           cm,
			Tools:           []tool.BaseTool{counter},
			CheckpointStore: checkpointer.NewInMemoryStore(),
			HITLConfig:      hitl,
		},
		CustomStateBuilder: func(ctx context.Context, threadID, turnID string) map[string]types.RunTimeStateful {
			return map[string]types.RunTimeStateful{
				"custom": &cs,
			}
		},
	}, hs, 8, nil, bus)
	if err := th.Init(ctx); err != nil {
		t.Fatalf("start err: %v", err)
	}

	coll := &eventCollector{}
	go coll.collect(bus)

	// 提交用户输入，预期产生审批请求事件
	runErrCh := runUserInputAsync(ctx, th, "rA", "go")

	// 等待审批请求事件
	if _, ok := coll.waitFor(t, EventApproveRequested, 3*time.Second); !ok {
		t.Fatalf("未收到 ApproveRequested 事件")
	}

	// 恢复运行：提供审批通过的 ResumeData
	// 获取最近中断 ID：简单从事件集中找到 ApproveRequested 载荷（测试中可简化）
	interruptID := ""
	checkpointID := ""
	coll.mu.Lock()
	for _, ev := range coll.events {
		if ev.Type == EventApproveRequested {
			p := ev.Payload.(ApprovalRequiredPayload)
			interruptID = p.InterruptID
			checkpointID = p.CheckpointID
			break
		}
	}
	coll.mu.Unlock()
	if interruptID == "" {
		t.Fatalf("未捕获到 InterruptID")
	}
	if checkpointID == "" {
		t.Fatalf("未捕获到 CheckpointID")
	}

	resume := map[string]any{interruptID: &deeptools.ApprovalResult{Approved: true}}
	if _, err := th.ResumeTurn(ctx, "rA", ResumeTurnOptions{CheckpointID: checkpointID, ResumeData: resume, ResumeInterruptIDs: []string{interruptID}}); err != nil {
		t.Fatalf("resume submit err: %v", err)
	}
	if err := <-runErrCh; err != nil {
		t.Fatalf("首次运行应通过事件表面化中断，不应返回错误: %v", err)
	}

	if _, ok := coll.waitFor(t, EventTurnEnd, 3*time.Second); !ok {
		t.Fatalf("恢复后未收到 TurnEnd")
	}

}

// 外部主动中断与恢复（单次 Stream 两个 chunk，Timeout=10ms）
func TestAgentThread_ExternalInterruptAndResume(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](2)
			time.Sleep(200 * time.Millisecond)
			sw.Send(schema.AssistantMessage("done", nil), nil)
			sw.Close()
			return sr, nil
		},
	).AnyTimes()

	bus := make(chan Event, 256)
	hs := NewInMemoryHistoryRolloutStore()
	th := buildThreadForTest("thread-ext", &TurnConfig{
		Agent: deepagents.Config{
			Model:               cm,
			Tools:               nil,
			CheckpointStore:     checkpointer.NewInMemoryStore(),
			InterruptAfterNodes: []string{"model"},
		},
	}, hs, 8, nil, bus)
	if err := th.Init(ctx); err != nil {
		t.Fatalf("start err: %v", err)
	}

	coll := &eventCollector{}
	go coll.collect(bus)

	// 提交用户输入启动慢速回合
	turnID := "ri"
	runErrCh := runUserInputAsync(ctx, th, turnID, "start")

	// 等待10ms后通过 SubmitOp 触发外部中断（Timeout=10ms，ExitLoop=false）
	time.Sleep(10 * time.Millisecond)
	timeout := 10 * time.Millisecond
	if ok := th.Interrupt(InterruptOptions{
		Timeout:  &timeout,
		Metadata: map[string]string{"reason": "test_interrupt"},
	}); !ok {
		t.Fatalf("submit interrupt err")
	}
	// 期望在 timeout+10ms 内收到中断事件
	ev, ok := coll.waitFor(t, EventInterrupted, timeout+30*time.Millisecond)
	if !ok {
		t.Fatalf("未在限定时间内捕获到中断事件")
	}
	payload, ok := ev.Payload.(InterruptedPayload)
	if !ok {
		t.Fatalf("unexpected interrupt payload: %T", ev.Payload)
	}
	if payload.Source != "external" || payload.TimeoutMS != timeout.Milliseconds() || payload.Metadata["reason"] != "test_interrupt" {
		t.Fatalf("external interrupt payload = %+v", payload)
	}
	// 恢复运行（无需 InterruptID，外部中断上下文为空）：提供 CheckpointID 即可
	if _, err := th.ResumeTurn(ctx, turnID, ResumeTurnOptions{CheckpointID: turnID}); err != nil {
		t.Fatalf("submit resume err: %v", err)
	}
	if err := <-runErrCh; err != nil {
		t.Fatalf("首次运行应通过事件表面化中断，不应返回错误: %v", err)
	}

	// 恢复后应完成回合（TurnEnd）
	if _, ok := coll.waitFor(t, EventTurnEnd, 2*time.Second); !ok {
		t.Fatalf("恢复后未收到 TurnEnd")
	}
	// 校验历史最后一条为助手消息 done
	recs, err := hs.List(ctx, ListQuery{ThreadID: th.ThreadID, Order: ListOrderASC})
	if err != nil {
		t.Fatalf("history list err: %v", err)
	}
	if len(recs) == 0 || recs[len(recs)-1].Message == nil || recs[len(recs)-1].Message.Role != schema.Assistant || recs[len(recs)-1].Message.Content != "done" {
		t.Fatalf("历史最后一条消息不正确，期望 assistant=done，实际：%v", recs[len(recs)-1].Message)
	}

}

// 开启 EnableStreamToolCall 后，如果第一个审批工具已经完整收集并触发中断，
// 但第二个 tool call 只吐出一半，那么恢复后只会继续第一个工具，未完整的后续调用不会恢复。
func TestAgentThread_StreamToolCall_ApproveResume_DropsIncompleteFutureCalls(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	store := checkpointer.NewInMemoryStore()
	approvedBase := &countingToolResult{name: "approval_tool", result: `{"approved":true}`}
	lostTool := &countingToolResult{name: "lost_tool", result: `{"lost":false}`}
	approvedTool := deeptools.NewInvokablePolicyTool(approvedBase, deeptools.ApprovalGate(func(context.Context, *deeptools.ApprovalInfo) bool {
		return true
	}))

	var streamCall int32
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](4)
			callIndex := atomic.AddInt32(&streamCall, 1)
			go func() {
				defer sw.Close()
				switch callIndex {
				case 1:
					idx0 := 0
					idx1 := 1
					sw.Send(&schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{{
							ID:    "approve_call",
							Index: &idx0,
							Function: schema.FunctionCall{
								Name:      "approval_tool",
								Arguments: `{"task":"approve"}`,
							},
						}},
					}, nil)
					sw.Send(&schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{{
							ID:    "lost_call",
							Index: &idx1,
							Function: schema.FunctionCall{
								Arguments: `{"task":"lo`,
							},
						}},
					}, nil)
				case 2:
					var toolMsgs []*schema.Message
					for _, msg := range input {
						if msg != nil && msg.Role == schema.Tool {
							toolMsgs = append(toolMsgs, msg)
						}
					}
					if len(toolMsgs) != 1 || toolMsgs[0].ToolCallID != "approve_call" {
						sw.Send(nil, fmt.Errorf("resume input expected only approve_call tool message, got %+v", input))
						return
					}
					sw.Send(schema.AssistantMessage("resume-only-first-tool", nil), nil)
				default:
					sw.Send(schema.AssistantMessage("unexpected", nil), nil)
				}
			}()
			return sr, nil
		},
	).AnyTimes()

	bus := make(chan Event, 256)
	hs := NewInMemoryHistoryRolloutStore()
	th := buildThreadForTest("thread-stream-partial", &TurnConfig{
		Agent: deepagents.Config{
			Model:                cm,
			Tools:                []tool.BaseTool{approvedTool, lostTool},
			CheckpointStore:      store,
			EnableStreamToolCall: true,
		},
	}, hs, 8, nil, bus)
	if err := th.Init(ctx); err != nil {
		t.Fatalf("start err: %v", err)
	}

	coll := &eventCollector{}
	go coll.collect(bus)

	turnID := "r1"
	runErrCh := runUserInputAsync(ctx, th, turnID, "run partial approval")
	ev, ok := coll.waitFor(t, EventApproveRequested, 3*time.Second)
	if !ok {
		t.Fatalf("未收到 ApproveRequested 事件")
	}
	p := ev.Payload.(ApprovalRequiredPayload)

	resume := map[string]any{p.InterruptID: &deeptools.ApprovalResult{Approved: true}}
	if _, err := th.ResumeTurn(ctx, turnID, ResumeTurnOptions{
		CheckpointID:       p.CheckpointID,
		ResumeInterruptIDs: []string{p.InterruptID},
		ResumeData:         resume,
	}); err != nil {
		t.Fatalf("resume submit err: %v", err)
	}
	if err := <-runErrCh; err != nil {
		t.Fatalf("首次运行应通过事件表面化中断，不应返回错误: %v", err)
	}
	waitUntilEventfully(t, 3*time.Second, func() bool {
		return approvedBase.RunCount() == 1 &&
			lostTool.RunCount() == 0 &&
			hasEventType(coll.snapshot(), EventTurnEnd)
	}, "恢复后未达到预期完成状态")

	if approvedBase.RunCount() != 1 {
		t.Fatalf("approved tool should execute exactly once after resume, got %d", approvedBase.RunCount())
	}
	if lostTool.RunCount() != 0 {
		t.Fatalf("incomplete future tool should not execute, got %d", lostTool.RunCount())
	}

	events := coll.snapshot()
	var lostStarts int
	for _, event := range events {
		if event.Type == EventToolStart {
			if payload, ok := event.Payload.(ToolStartPayload); ok && payload.CallID == "lost_call" {
				lostStarts++
			}
		}
	}
	if lostStarts != 0 {
		t.Fatalf("lost_call should never start, got %d starts", lostStarts)
	}
}

// 开启 EnableStreamToolCall 后，如果 4 个 tool call 都已经完整进入 tools 执行器，
// 且前 3 个完成、最后 1 个审批中断，那么恢复后只会补执行最后 1 个。
func TestAgentThread_StreamToolCall_ApproveResume_RerunsOnlyInterruptedCall(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	store := checkpointer.NewInMemoryStore()
	tool1 := &countingToolResult{name: "plain_tool_1", result: `{"tool":1}`}
	tool2 := &countingToolResult{name: "plain_tool_2", result: `{"tool":2}`}
	tool3 := &countingToolResult{name: "plain_tool_3", result: `{"tool":3}`}
	tool4Base := &countingToolResult{name: "approval_tool_4", result: `{"tool":4}`}
	tool4 := deeptools.NewInvokablePolicyTool(tool4Base, deeptools.ApprovalGate(func(context.Context, *deeptools.ApprovalInfo) bool {
		return true
	}))

	var streamCall int32
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](4)
			callIndex := atomic.AddInt32(&streamCall, 1)
			go func() {
				defer sw.Close()
				switch callIndex {
				case 1:
					sw.Send(&schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{
							{ID: "call_1", Function: schema.FunctionCall{Name: "plain_tool_1", Arguments: `{"n":1}`}},
							{ID: "call_2", Function: schema.FunctionCall{Name: "plain_tool_2", Arguments: `{"n":2}`}},
							{ID: "call_3", Function: schema.FunctionCall{Name: "plain_tool_3", Arguments: `{"n":3}`}},
							{ID: "call_4", Function: schema.FunctionCall{Name: "approval_tool_4", Arguments: `{"n":4}`}},
						},
					}, nil)
				case 2:
					got := map[string]string{}
					for _, msg := range input {
						if msg != nil && msg.Role == schema.Tool {
							got[msg.ToolCallID] = msg.Content
						}
					}
					if len(got) != 4 {
						sw.Send(nil, fmt.Errorf("expected 4 tool messages after resume, got %+v", input))
						return
					}
					if got["call_1"] != `{"tool":1}` || got["call_2"] != `{"tool":2}` || got["call_3"] != `{"tool":3}` || got["call_4"] != `{"tool":4}` {
						sw.Send(nil, fmt.Errorf("unexpected resume tool messages: %+v", got))
						return
					}
					sw.Send(schema.AssistantMessage("resume-rerun-last-only", nil), nil)
				default:
					sw.Send(schema.AssistantMessage("unexpected", nil), nil)
				}
			}()
			return sr, nil
		},
	).AnyTimes()

	bus := make(chan Event, 256)
	hs := NewInMemoryHistoryRolloutStore()
	th := buildThreadForTest("thread-stream-full", &TurnConfig{
		Agent: deepagents.Config{
			Model:                cm,
			Tools:                []tool.BaseTool{tool1, tool2, tool3, tool4},
			CheckpointStore:      store,
			EnableStreamToolCall: true,
		},
	}, hs, 8, nil, bus)
	if err := th.Init(ctx); err != nil {
		t.Fatalf("start err: %v", err)
	}

	coll := &eventCollector{}
	go coll.collect(bus)

	turnID := "r1"
	runErrCh := runUserInputAsync(ctx, th, turnID, "run full approval")
	ev, ok := coll.waitFor(t, EventApproveRequested, 3*time.Second)
	if !ok {
		t.Fatalf("未收到 ApproveRequested 事件")
	}
	p := ev.Payload.(ApprovalRequiredPayload)

	resume := map[string]any{p.InterruptID: &deeptools.ApprovalResult{Approved: true}}
	if _, err := th.ResumeTurn(ctx, turnID, ResumeTurnOptions{
		CheckpointID: p.CheckpointID,
		ResumeData:   resume,
	}); err != nil {
		t.Fatalf("resume submit err: %v", err)
	}
	if err := <-runErrCh; err != nil {
		t.Fatalf("首次运行应通过事件表面化中断，不应返回错误: %v", err)
	}
	waitUntilEventfully(t, 3*time.Second, func() bool {
		return tool1.RunCount() == 1 &&
			tool2.RunCount() == 1 &&
			tool3.RunCount() == 1 &&
			tool4Base.RunCount() == 1 &&
			atomic.LoadInt32(&streamCall) >= 2 &&
			hasEventType(coll.snapshot(), EventTurnEnd)
	}, "恢复后未达到预期完成状态")

	if tool1.RunCount() != 1 || tool2.RunCount() != 1 || tool3.RunCount() != 1 {
		t.Fatalf("completed tools should not rerun, got counts: %d %d %d", tool1.RunCount(), tool2.RunCount(), tool3.RunCount())
	}
	if tool4Base.RunCount() != 1 {
		t.Fatalf("approval tool should execute exactly once after approval, got %d", tool4Base.RunCount())
	}
}
