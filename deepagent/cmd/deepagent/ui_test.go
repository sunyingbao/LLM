package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/middleware/planmode"
	"eino-cli/deepagent/worker"
	inprocess "eino-cli/deepagent/worker/inprocess"
	"eino-cli/deepagent/worker/tasktool"
	tea "github.com/charmbracelet/bubbletea"
)

func TestParseResumeChoice(t *testing.T) {
	idx, ok := parseResumeChoice("2", 3)
	if !ok || idx != 1 {
		t.Fatalf("parseResumeChoice() = %d, %t; want 1, true", idx, ok)
	}
	if _, ok := parseResumeChoice("4", 3); ok {
		t.Fatalf("parseResumeChoice() accepted out-of-range choice")
	}
}

func TestCtrlCQuitsEvenWhenApprovalPending(t *testing.T) {
	model := testChatModel()
	model.pending = &pendingApproval{ThreadID: "main", TurnID: "turn_1"}

	_, cmd := model.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected quit command")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("cmd() = %T, want tea.QuitMsg", msg)
	}
}

func TestSlashInputShowsCommandHints(t *testing.T) {
	model := testChatModel()
	model.input = "/"

	panel := model.renderPanel(100)
	if !strings.Contains(panel, "/resume") || !strings.Contains(panel, "/threads") {
		t.Fatalf("command hints missing:\n%s", panel)
	}
}

func TestSlashCommandHintKeepsSwitchWithArgument(t *testing.T) {
	model := testChatModel()
	model.input = "/switch ed7953bf"

	panel := model.renderPanel(100)
	if strings.Contains(panel, "no matching command") || !strings.Contains(panel, "/switch <thread>") {
		t.Fatalf("switch hint should remain visible for arguments:\n%s", panel)
	}
}

func TestViewDoesNotRenderTranscriptHistory(t *testing.T) {
	model := testChatModel()
	model.width = 100
	model.height = 20
	model.append(lineAssistant, "old transcript line")

	view := model.View()
	if strings.Contains(view, "old transcript line") {
		t.Fatalf("transcript history should not be redrawn in View:\n%s", view)
	}
	if len(model.printQueue) == 0 || !strings.Contains(strings.Join(model.printQueue, "\n"), "old transcript line") {
		t.Fatalf("transcript line should be queued for terminal print: %+v", model.printQueue)
	}
}

func TestViewDoesNotRenderTopHeader(t *testing.T) {
	model := testChatModel()
	model.width = 100
	model.height = 20

	view := model.View()
	if strings.Contains(view, "DeepAgent Local") {
		t.Fatalf("top header should not be rendered:\n%s", view)
	}
	if !strings.Contains(view, "status=ready") {
		t.Fatalf("status line missing:\n%s", view)
	}
}

func TestStatusLineCommandTogglesFields(t *testing.T) {
	model := testChatModel()
	model.handleStatusLineCommand([]string{"hide", "session"})
	if strings.Contains(model.renderStatus(120), "session=") {
		t.Fatalf("session should be hidden:\n%s", model.renderStatus(120))
	}
	model.handleStatusLineCommand([]string{"show", "active"})
	if !strings.Contains(model.renderStatus(120), "active=main") {
		t.Fatalf("active should be shown:\n%s", model.renderStatus(120))
	}
}

func TestStatusLineContextUsageUpdatesFromLLMEnd(t *testing.T) {
	model := testChatModel()
	model.contextWindow = 1000
	model.applyRealtimeEvent(testEvent("ev_ctx", "main", "turn_1", agentworker.EventType(agentthread.EventLLMEnd), localEventPayload{TotalTokens: 250}))

	status := model.renderStatus(120)
	if !strings.Contains(status, "ctx=250/1.0k 25%") {
		t.Fatalf("context usage missing:\n%s", status)
	}
}

func TestStatusLineUsesActiveThreadActivity(t *testing.T) {
	model := testChatModel()
	model.applyRealtimeEvent(testEvent("ev_token", "main", "turn_1", agentworker.EventType(agentthread.EventLLMToken), localEventPayload{Text: "hello"}))
	if status := model.renderStatus(120); !strings.Contains(status, "status=thinking") {
		t.Fatalf("status should use active thread activity:\n%s", status)
	}
	model.applyRealtimeEvent(testEvent("ev_tool", "main", "turn_1", agentworker.EventType(agentthread.EventToolStart), localEventPayload{Name: "ls"}))
	if status := model.renderStatus(120); !strings.Contains(status, "status=tool:ls") {
		t.Fatalf("status should show active tool:\n%s", status)
	}
	model.applyRealtimeEvent(testEvent("ev_end", "main", "turn_1", agentworker.EventType(agentthread.EventTurnEnd), localEventPayload{}))
	if status := model.renderStatus(120); !strings.Contains(status, "status=ready") {
		t.Fatalf("status should return to ready:\n%s", status)
	}
}

func TestChatPreservesScrollPositionWhenNewLineArrives(t *testing.T) {
	model := testChatModel()
	model.width = 80
	model.height = 12
	for i := 0; i < 20; i++ {
		model.append(lineEvent, "line "+strconv.Itoa(i))
	}
	model.scrollBy(6)
	before := model.scrollOffset
	model.append(lineEvent, "new line")
	if model.scrollOffset < before {
		t.Fatalf("scrollOffset = %d, want >= %d", model.scrollOffset, before)
	}
}

func TestResumeModeSupportsArrowSelection(t *testing.T) {
	model := testChatModel()
	model.resumeMode = true
	model.resumeList = []*inprocess.ThreadState{
		{ID: "thread_1", SessionID: "sess_1", Title: "one"},
		{ID: "thread_2", SessionID: "sess_2", Title: "two"},
	}

	next, _ := model.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	updated := next.(chatModel)
	if updated.resumeIndex != 1 {
		t.Fatalf("resumeIndex = %d, want 1", updated.resumeIndex)
	}
	panel := updated.renderPanel(100)
	if !strings.Contains(panel, "> 2.") {
		t.Fatalf("selected resume item missing:\n%s", panel)
	}
}

func TestPlanInputEnterAdvancesBeforeSubmitting(t *testing.T) {
	model := testChatModel()
	model.planPending = &pendingPlanInput{
		Key:         "main:turn_1:interrupt_1",
		ThreadID:    "main",
		TurnID:      "turn_1",
		InterruptID: "interrupt_1",
		Questions: []planmode.Question{
			testPlanQuestion("area", "Area", "Choose an area."),
			testPlanQuestion("risk", "Risk", "Choose a risk level."),
		},
	}
	model.planPending.ensureSelected()

	next, cmd := model.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("first question enter should not submit the request")
	}
	updated := next.(chatModel)
	if updated.planPending.ActiveQuestion != 1 {
		t.Fatalf("ActiveQuestion = %d, want 1", updated.planPending.ActiveQuestion)
	}
	if !updated.planPending.Answered[0] || updated.planPending.Answers[0] != "Recommended" {
		t.Fatalf("first answer not committed: answered=%v answer=%q", updated.planPending.Answered[0], updated.planPending.Answers[0])
	}
	if updated.planPending.Answered[1] {
		t.Fatal("second answer should not be committed yet")
	}

	updated.setPlanSelection(1)
	next, cmd = updated.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("last question enter should submit all answers")
	}
	updated = next.(chatModel)
	if !updated.planPending.Answered[1] || updated.planPending.Answers[1] != "Alternative" {
		t.Fatalf("second answer not committed: answered=%v answer=%q", updated.planPending.Answered[1], updated.planPending.Answers[1])
	}
}

func TestChatTitleFromMessage(t *testing.T) {
	got := chatTitleFromMessage("hello\nworld")
	if got != "hello world" {
		t.Fatalf("chatTitleFromMessage() = %q", got)
	}
}

func TestChatTitleFromMessageIsUnicodeSafe(t *testing.T) {
	got := chatTitleFromMessage(strings.Repeat("中文", 40))
	if !utf8.ValidString(got) {
		t.Fatalf("chatTitleFromMessage() returned invalid utf8: %q", got)
	}
	if len([]rune(got)) > 63 {
		t.Fatalf("chatTitleFromMessage() rune len = %d, want <= 63", len([]rune(got)))
	}
}

func TestRealtimeActiveTokensFlushOnce(t *testing.T) {
	model := testChatModel()
	model.applyRealtimeEvent(testEvent("ev_1", "main", "turn_1", agentworker.EventType(agentthread.EventLLMToken), localEventPayload{Text: "hello "}))
	model.applyRealtimeEvent(testEvent("ev_2", "main", "turn_1", agentworker.EventType(agentthread.EventLLMToken), localEventPayload{Text: "world"}))
	model.applyRealtimeEvent(testEvent("ev_3", "main", "turn_1", agentworker.EventType(agentthread.EventLLMEnd), localEventPayload{Message: "hello world"}))
	model.applyRealtimeEvent(testEvent("ev_4", "main", "turn_1", agentworker.EventType(agentthread.EventTurnEnd), localEventPayload{}))

	if got := assistantLines(model.lines); len(got) != 1 || got[0] != "hello world" {
		t.Fatalf("assistant lines = %#v, want single final draft", got)
	}
}

func TestRealtimeActiveDraftIsRenderableBeforeTurnEnd(t *testing.T) {
	model := testChatModel()
	model.applyRealtimeEvent(testEvent("ev_1", "main", "turn_1", agentworker.EventType(agentthread.EventLLMToken), localEventPayload{Text: "hello"}))

	got := model.appendActiveDraftLines(nil)
	if len(got) != 1 || got[0].Kind != lineAssistant || got[0].Text != "hello" {
		t.Fatalf("active draft lines = %+v, want assistant draft", got)
	}
}

func TestRealtimeReasoningDraftIsRenderable(t *testing.T) {
	model := testChatModel()
	model.applyRealtimeEvent(testEvent("ev_1", "main", "turn_1", agentworker.EventType(agentthread.EventLLMToken), localEventPayload{
		ReasoningText: "thinking",
	}))

	got := model.appendActiveDraftLines(nil)
	if len(got) != 1 || got[0].Kind != lineReasoning || got[0].Text != "thinking" {
		t.Fatalf("active reasoning lines = %+v, want reasoning draft", got)
	}
}

func TestRealtimeReasoningDraftDisappearsAfterAssistantText(t *testing.T) {
	model := testChatModel()
	model.applyRealtimeEvent(testEvent("ev_1", "main", "turn_1", agentworker.EventType(agentthread.EventLLMToken), localEventPayload{
		ReasoningText: "thinking",
	}))
	model.applyRealtimeEvent(testEvent("ev_2", "main", "turn_1", agentworker.EventType(agentthread.EventLLMToken), localEventPayload{
		Text: "answer",
	}))

	got := model.appendActiveDraftLines(nil)
	if len(got) != 1 || got[0].Kind != lineAssistant || got[0].Text != "answer" {
		t.Fatalf("active draft lines = %+v, want assistant text only", got)
	}
}

func TestReasoningIsNotPersistedToTranscript(t *testing.T) {
	model := testChatModel()
	model.applyRealtimeEvent(testEvent("ev_1", "main", "turn_1", agentworker.EventType(agentthread.EventLLMToken), localEventPayload{ReasoningText: "thinking"}))
	model.applyRealtimeEvent(testEvent("ev_2", "main", "turn_1", agentworker.EventType(agentthread.EventLLMEnd), localEventPayload{
		Message:       "final",
		ReasoningText: "full reasoning",
	}))
	model.applyRealtimeEvent(testEvent("ev_3", "main", "turn_1", agentworker.EventType(agentthread.EventTurnEnd), localEventPayload{}))

	if got := reasoningLines(model.lines); len(got) != 0 {
		t.Fatalf("reasoning transcript lines = %#v, want none", got)
	}
	if got := assistantLines(model.lines); len(got) != 1 || got[0] != "final" {
		t.Fatalf("assistant lines = %#v, want final only", got)
	}
}

func TestHistoricalReplayIgnoresTokens(t *testing.T) {
	model := testChatModel()
	model.applyHistoricalEvent(testEvent("ev_1", "main", "turn_1", agentworker.EventType(agentthread.EventLLMToken), localEventPayload{Text: "partial"}))
	model.applyHistoricalEvent(testEvent("ev_2", "main", "turn_1", agentworker.EventType(agentthread.EventLLMEnd), localEventPayload{Message: "final"}))

	if got := assistantLines(model.lines); len(got) != 1 || got[0] != "final" {
		t.Fatalf("assistant lines = %#v, want historical final only", got)
	}
}

func TestHistoricalReplayDoesNotOfferPlanAction(t *testing.T) {
	model := testChatModel()
	model.applyHistoricalEvent(testEvent("ev_plan", "main", "turn_plan", agentworker.EventType(agentthread.EventLLMEnd), localEventPayload{
		Message: "<proposed_plan>\nDo it.\n</proposed_plan>",
	}))

	if model.planAction != nil {
		t.Fatalf("historical proposed plan should not reopen plan action: %+v", model.planAction)
	}
	if got := assistantLines(model.lines); len(got) != 1 || !strings.Contains(got[0], "<proposed_plan>") {
		t.Fatalf("assistant lines = %#v, want historical plan rendered", got)
	}
}

func TestHistoricalReplayKeepsAssistantSegmentsWithinTurn(t *testing.T) {
	model := testChatModel()
	model.applyHistoricalEvents([]*agentworker.Event{
		testEvent("ev_1", "main", "turn_1", agentworker.EventType(agentthread.EventLLMEnd), localEventPayload{Message: "intermediate"}),
		testEvent("ev_2", "main", "turn_1", agentworker.EventType(agentthread.EventToolStart), localEventPayload{Name: "wait_message"}),
		testEvent("ev_3", "main", "turn_1", agentworker.EventType(agentthread.EventLLMEnd), localEventPayload{Message: "final"}),
		testEvent("ev_4", "main", "turn_1", agentworker.EventType(agentthread.EventTurnEnd), localEventPayload{}),
	})

	if got := assistantLines(model.lines); len(got) != 2 || got[0] != "intermediate" || got[1] != "final" {
		t.Fatalf("assistant lines = %#v, want intermediate and final", got)
	}
}

func TestHistoricalToolEndShowsFallbackToolCall(t *testing.T) {
	model := testChatModel()
	model.applyHistoricalEvents([]*agentworker.Event{
		testEvent("ev_1", "main", "turn_1", agentworker.EventType(agentthread.EventToolEnd), localEventPayload{
			Name:            "exec_command",
			ArgumentsInJSON: `{"cmd":"mkdir -p cuda_reduce_demo/src"}`,
			Result:          `{"command":["/bin/bash","-lc","mkdir -p cuda_reduce_demo/src"],"exit_code":0}`,
		}),
	})

	if got := toolLines(model.lines); len(got) != 1 || got[0] != "Run mkdir -p cuda_reduce_demo/src" {
		t.Fatalf("tool lines = %#v", got)
	}
}

func TestToolFormattingUsesReadableSummaryAndResult(t *testing.T) {
	if got := formatToolStartSummary("ls", `{"path":"cuda_reduce_demo"}`); got != "List cuda_reduce_demo" {
		t.Fatalf("ls summary = %q", got)
	}
	lsResult := `{"data":[{"path":"cuda_reduce_demo/src","is_dir":true},{"path":"cuda_reduce_demo/README.md","is_dir":false}],"errmsg":""}`
	if got := formatToolResultDetail("ls", "", lsResult); got != "2 entries: cuda_reduce_demo/src/, cuda_reduce_demo/README.md" {
		t.Fatalf("ls result = %q", got)
	}
	if got := formatToolStartSummary("read_file", `{"path":"cuda_reduce_demo/README.md","offset":10,"limit":20}`); got != "Read cuda_reduce_demo/README.md (offset=10 limit=20)" {
		t.Fatalf("read_file summary = %q", got)
	}
	readResult := `{"data":"# CUDA Reduce Demo\n\nbody","errmsg":""}`
	if got := formatToolResultDetail("read_file", "", readResult); got != "3 lines, preview: # CUDA Reduce Demo" {
		t.Fatalf("read_file result = %q", got)
	}
	if got := formatToolStartSummary("wait_message", `{"target":"af555442-4888-408f-9dbe-63472a7fd2a6","message_id":"msg_504d486a-e374-4109-8ed5-9846a89ac8b9"}`); got != "Wait for af555442-4888-408f-9dbe-63472a7fd2a6 / msg_504d" {
		t.Fatalf("wait_message summary = %q", got)
	}
	if got := formatToolStartSummary("spawn_task", `{"title":"探索cuda_reduce_demo代码库","role":"explorer"}`); got != "Spawn explorer: 探索cuda_reduce_demo代码库" {
		t.Fatalf("spawn_task summary = %q", got)
	}
}

func TestRealtimeToolEndShowsResultDetail(t *testing.T) {
	model := testChatModel()
	model.applyRealtimeEvent(testEvent("ev_1", "main", "turn_1", agentworker.EventType(agentthread.EventToolStart), localEventPayload{
		Name: "ls",
		Args: `{"path":"."}`,
	}))
	model.applyRealtimeEvent(testEvent("ev_2", "main", "turn_1", agentworker.EventType(agentthread.EventToolEnd), localEventPayload{
		Name:   "ls",
		Result: `{"data":[{"path":"cuda_reduce_demo","is_dir":true}],"errmsg":""}`,
	}))

	if got := toolLines(model.lines); len(got) != 1 || got[0] != "List ." {
		t.Fatalf("tool lines = %#v", got)
	}
	if got := detailLines(model.lines); len(got) != 1 || got[0] != "1 entry: cuda_reduce_demo/" {
		t.Fatalf("detail lines = %#v", got)
	}
}

func TestUpdatePlanToolEndShowsPlan(t *testing.T) {
	model := testChatModel()
	args := `{"plan":[{"step":"Inspect existing execute/HITL tests and cmd approval reuse tests","status":"completed"},{"step":"Remove internal approval denial from ExecuteMiddleware.run","status":"completed"},{"step":"Run targeted tests","status":"pending"}]}`
	model.applyRealtimeEvent(testEvent("ev_1", "main", "turn_1", agentworker.EventType(agentthread.EventToolStart), localEventPayload{
		Name: "update_plan",
		Args: args,
	}))
	model.applyRealtimeEvent(testEvent("ev_2", "main", "turn_1", agentworker.EventType(agentthread.EventToolEnd), localEventPayload{
		Name:            "update_plan",
		ArgumentsInJSON: args,
		Result:          "Plan updated",
	}))

	if got := toolLines(model.lines); len(got) != 1 || got[0] != "Updated Plan" {
		t.Fatalf("tool lines = %#v", got)
	}
	want := "✔ Inspect existing execute/HITL tests and cmd approval reuse tests\n✔ Remove internal approval denial from ExecuteMiddleware.run\n□ Run targeted tests"
	if got := detailLines(model.lines); len(got) != 1 || got[0] != want {
		t.Fatalf("detail lines = %#v", got)
	}
}

func TestHistoricalUpdatePlanToolEndShowsPlan(t *testing.T) {
	model := testChatModel()
	args := `{"plan":[{"step":"Inspect current tool rendering hooks","status":"completed"},{"step":"Add update_plan result formatter","status":"in_progress"}]}`
	model.applyHistoricalEvents([]*agentworker.Event{
		testEvent("ev_1", "main", "turn_1", agentworker.EventType(agentthread.EventToolEnd), localEventPayload{
			Name:            "update_plan",
			ArgumentsInJSON: args,
			Result:          "Plan updated",
		}),
	})

	if got := toolLines(model.lines); len(got) != 1 || got[0] != "Updated Plan" {
		t.Fatalf("tool lines = %#v", got)
	}
	if got := detailLines(model.lines); len(got) != 1 || got[0] != "✔ Inspect current tool rendering hooks\n→ Add update_plan result formatter" {
		t.Fatalf("detail lines = %#v", got)
	}
}

func TestToolStartDiscardsIntermediateDraft(t *testing.T) {
	model := testChatModel()
	model.applyRealtimeEvent(testEvent("ev_1", "main", "turn_1", agentworker.EventType(agentthread.EventLLMToken), localEventPayload{Text: "intermediate"}))
	model.applyRealtimeEvent(testEvent("ev_2", "main", "turn_1", agentworker.EventType(agentthread.EventToolStart), localEventPayload{Name: "wait_message"}))
	model.applyRealtimeEvent(testEvent("ev_3", "main", "turn_1", agentworker.EventType(agentthread.EventLLMToken), localEventPayload{Text: "final"}))
	model.applyRealtimeEvent(testEvent("ev_4", "main", "turn_1", agentworker.EventType(agentthread.EventTurnEnd), localEventPayload{}))

	if got := assistantLines(model.lines); len(got) != 1 || got[0] != "final" {
		t.Fatalf("assistant lines = %#v, want intermediate draft discarded", got)
	}
}

func TestSidecarChildTokensAreHiddenFromTranscript(t *testing.T) {
	model := testChatModel()
	model.rememberThread(&inprocess.ThreadState{ID: "child", SessionID: "sess", ParentThreadID: "main", Title: "child"})
	model.applyRealtimeEvent(testEvent("ev_1", "child", "turn_child", agentworker.EventType(agentthread.EventLLMToken), localEventPayload{Text: "child token"}))
	model.applyRealtimeEvent(testEvent("ev_2", "child", "turn_child", agentworker.EventType(agentthread.EventLLMEnd), localEventPayload{Message: "child final"}))

	if got := assistantLines(model.lines); len(got) != 0 {
		t.Fatalf("assistant lines = %#v, want child transcript hidden", got)
	}
	if status := threadStatus(model.threadViews["child"]); status == "" {
		t.Fatalf("child status was not updated")
	}
}

func TestHistoricalStaleApprovalDoesNotRestorePending(t *testing.T) {
	model := testChatModel()
	model.applyHistoricalEvents([]*agentworker.Event{
		testEvent("ev_1", "main", "turn_1", agentworker.EventType(agentthread.EventApproveRequested), localEventPayload{
			InterruptID:        "interrupt_1",
			CheckpointID:       "ckpt_1",
			ToolName:           "exec_command",
			ArgumentsInJSON:    `{"cmd":"mkdir -p demo"}`,
			ConsumedMessageIDs: []string{"msg_1"},
		}),
	})

	if model.pending != nil || len(model.pendingApprovals) != 0 {
		t.Fatalf("stale approval restored: pending=%+v all=%+v", model.pending, model.pendingApprovals)
	}
}

func TestHistoricalBlockedThreadRestoresApproval(t *testing.T) {
	model := testChatModel()
	model.active.PendingBlock = &agentworker.PendingBlock{
		TurnID:       "turn_1",
		InterruptID:  "interrupt_1",
		CheckpointID: "ckpt_1",
		Kind:         "approval",
	}
	model.rememberThread(model.active)
	model.applyHistoricalEvents([]*agentworker.Event{
		testEvent("ev_1", "main", "turn_1", agentworker.EventType(agentthread.EventApproveRequested), localEventPayload{
			InterruptID:     "interrupt_1",
			CheckpointID:    "ckpt_1",
			ToolName:        "exec_command",
			ArgumentsInJSON: `{"cmd":"mkdir -p demo"}`,
		}),
	})

	if model.pending == nil || model.pending.InterruptID != "interrupt_1" {
		t.Fatalf("pending approval not restored: %+v", model.pending)
	}
}

func TestHistoricalToolEndClearsApprovalForTurn(t *testing.T) {
	model := testChatModel()
	model.active.PendingBlock = &agentworker.PendingBlock{
		TurnID:       "turn_1",
		InterruptID:  "interrupt_1",
		CheckpointID: "ckpt_1",
		Kind:         "approval",
	}
	model.rememberThread(model.active)
	model.applyHistoricalEvents([]*agentworker.Event{
		testEvent("ev_1", "main", "turn_1", agentworker.EventType(agentthread.EventApproveRequested), localEventPayload{
			InterruptID:  "interrupt_1",
			CheckpointID: "ckpt_1",
			ToolName:     "exec_command",
		}),
		testEvent("ev_2", "main", "turn_1", agentworker.EventType(agentthread.EventToolEnd), localEventPayload{Name: "exec_command"}),
	})

	if model.pending != nil || len(model.pendingApprovals) != 0 {
		t.Fatalf("approval should be cleared: pending=%+v all=%+v", model.pending, model.pendingApprovals)
	}
}

func TestAppendKnownThreadsIsVisible(t *testing.T) {
	model := testChatModel()
	model.rememberThread(&inprocess.ThreadState{ID: "child", SessionID: "sess", ParentThreadID: "main", Title: "child"})
	model.appendKnownThreads()

	var text string
	for _, line := range model.lines {
		text += line.Text + "\n"
	}
	if !strings.Contains(text, "current session threads") || !strings.Contains(text, "child") {
		t.Fatalf("thread output missing, got:\n%s", text)
	}
}

func TestFormatToolStartSummaryExecute(t *testing.T) {
	got := formatToolStartSummary("exec_command", `{"cmd":"mkdir -p cuda_reduce_demo/src"}`)
	if got != "Run mkdir -p cuda_reduce_demo/src" {
		t.Fatalf("formatToolStartSummary() = %q", got)
	}
}

func TestToolFormattingForRipgrepGrepAndApplyPatch(t *testing.T) {
	if got := formatToolStartSummary("exec_command", `{"cmd":"rg --files | rg '\\.go$'"}`); got != "Find files with rg: rg --files | rg '\\.go$'" {
		t.Fatalf("rg --files summary = %q", got)
	}
	if got := formatToolStartSummary("exec_command", `{"cmd":"rg TODO cmd/deepagent"}`); got != "Search with rg: rg TODO cmd/deepagent" {
		t.Fatalf("rg summary = %q", got)
	}

	grepResult := `{"data":[{"path":"cmd/deepagent/ui.go","line":10,"text":"func render() {}"},{"path":"cmd/deepagent/main.go","line":20,"text":"func main() {}"}],"errmsg":""}`
	if got := formatToolResultDetail("grep", "", grepResult); got != "2 matches: cmd/deepagent/ui.go:10: func render() {}; cmd/deepagent/main.go:20: func main() {}" {
		t.Fatalf("grep result = %q", got)
	}

	args := `{"patch":"*** Begin Patch\n*** Update File: cmd/deepagent/ui.go\n@@\n-old\n+new\n*** Add File: cmd/deepagent/new.go\n+package main\n*** End Patch\n"}`
	if got := formatToolStartSummary("apply_patch", args); got != "Edited 2 files (+2 -1)" {
		t.Fatalf("apply_patch summary = %q", got)
	}
	result := `{"data":"stdout>\nDone!\nstdout end\n","errmsg":""}`
	wantPatchResult := strings.Join([]string{
		"Edited cmd/deepagent/ui.go (+1 -1)",
		"  -old",
		"  +new",
		"",
		"Added cmd/deepagent/new.go (+1)",
		"  +package main",
	}, "\n")
	if got := formatToolResultDetail("apply_patch", args, result); got != wantPatchResult {
		t.Fatalf("apply_patch result = %q", got)
	}

	singleArgs := `{"patch":"*** Begin Patch\n*** Update File: cuda_reduce_demo/include/cuda_reduce.h\n@@\n-void old();\n+void new();\n*** End Patch\n"}`
	if got := formatToolStartSummary("apply_patch", singleArgs); got != "Edited cuda_reduce_demo/include/cuda_reduce.h (+1 -1)" {
		t.Fatalf("single apply_patch summary = %q", got)
	}
}

func TestApplyPatchResultUsesWorkDirLineNumbers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ui.go"), []byte(strings.Join([]string{
		"package main",
		"",
		"func render() {",
		"\tnewCall()",
		"}",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	args := `{"patch":"*** Begin Patch\n*** Update File: ui.go\n@@\n func render() {\n-\toldCall()\n+\tnewCall()\n }\n*** End Patch\n"}`
	result := `{"data":"stdout>\nSuccess. Updated the following files:\nM ui.go\nstdout end\n","errmsg":""}`
	want := strings.Join([]string{
		"3  func render() {",
		"4 -\toldCall()",
		"4 +\tnewCall()",
		"5  }",
	}, "\n")
	if got := formatApplyPatchResultWithWorkDir(args, result, dir); got != want {
		t.Fatalf("apply_patch line preview = %q", got)
	}
}

func TestFormatWaitMessageResultShowsState(t *testing.T) {
	completed := `{"data":{"res":{"af555442-4888/msg_b4074a63":{"result":"child report","done":true,"timed_out":false,"state":"completed"}}},"errmsg":""}`
	if got := formatWaitMessageResult(completed); got != "af555442/msg_b407 completed: child report" {
		t.Fatalf("completed wait result = %q", got)
	}
	timedOut := `{"data":{"res":{"af555442-4888/msg_b4074a63":{"done":false,"timed_out":true,"state":"waiting"}}},"errmsg":""}`
	if got := formatWaitMessageResult(timedOut); got != "af555442/msg_b407 timed out, state=waiting" {
		t.Fatalf("timed out wait result = %q", got)
	}
}

func TestLocalMessageWaitObserverApprovalIsNotTerminal(t *testing.T) {
	payload, _ := json.Marshal(localEventPayload{ConsumedMessageIDs: []string{"msg_1"}})
	got := localMessageWaitObserver([]*tasktool.Event{{
		Type:    string(agentthread.EventApproveRequested),
		TurnID:  "turn_1",
		Payload: payload,
	}}, "msg_1")
	if got.Done {
		t.Fatalf("approval should not complete wait_message: %+v", got)
	}
	if got.State != tasktool.WaitMessageStateApprovalRequired {
		t.Fatalf("approval state = %q", got.State)
	}
	if !strings.Contains(got.Result, "waiting for approval") {
		t.Fatalf("approval hint missing: %+v", got)
	}
}

func TestLocalMessageWaitObserverTerminalStateOverridesEarlierApproval(t *testing.T) {
	approvalPayload, _ := json.Marshal(localEventPayload{ConsumedMessageIDs: []string{"msg_1"}})
	errorPayload, _ := json.Marshal(localEventPayload{
		ConsumedMessageIDs: []string{"msg_1"},
		Message:            "resume failed",
	})
	got := localMessageWaitObserver([]*tasktool.Event{
		{
			Type:    string(agentthread.EventApproveRequested),
			TurnID:  "turn_1",
			Payload: approvalPayload,
		},
		{
			Type:    string(agentthread.EventError),
			TurnID:  "turn_1",
			Payload: errorPayload,
		},
	}, "msg_1")
	if !got.Done || got.State != tasktool.WaitMessageStateFailed || got.Result != "resume failed" {
		t.Fatalf("localMessageWaitObserver() = %+v", got)
	}
}

func TestLocalMessageWaitObserverUsesTargetTurnAfterMessageMatch(t *testing.T) {
	turnStartPayload, _ := json.Marshal(localEventPayload{ConsumedMessageIDs: []string{"msg_1"}})
	llmEndPayload, _ := json.Marshal(localEventPayload{Message: "child result"})
	turnEndPayload, _ := json.Marshal(localEventPayload{})
	got := localMessageWaitObserver([]*tasktool.Event{
		{
			Type:    string(agentthread.EventTurnStart),
			TurnID:  "turn_1",
			Payload: turnStartPayload,
		},
		{
			Type:    string(agentthread.EventLLMEnd),
			TurnID:  "turn_1",
			Payload: llmEndPayload,
		},
		{
			Type:    string(agentthread.EventTurnEnd),
			TurnID:  "turn_1",
			Payload: turnEndPayload,
		},
	}, "msg_1")
	if !got.Done || got.State != tasktool.WaitMessageStateCompleted || got.Result != "child result" {
		t.Fatalf("localMessageWaitObserver() = %+v", got)
	}
}

func testChatModel() chatModel {
	model := newChatModel(context.Background(), nil)
	model.lines = nil
	model.active = &inprocess.ThreadState{ID: "main", SessionID: "sess", Title: "main"}
	model.rememberThread(model.active)
	return model
}

func testPlanQuestion(id, header, question string) planmode.Question {
	return planmode.Question{
		ID:       id,
		Header:   header,
		Question: question,
		Options: []planmode.QuestionOption{
			{Label: "Recommended", Description: "Use the recommended path."},
			{Label: "Alternative", Description: "Use the alternative path."},
		},
	}
}

func testEvent(id, threadID, turnID string, eventType agentworker.EventType, payload localEventPayload) *agentworker.Event {
	data, _ := json.Marshal(payload)
	return &agentworker.Event{
		ID:       id,
		ThreadID: threadID,
		TurnID:   turnID,
		Type:     eventType,
		Payload:  data,
	}
}

func assistantLines(lines []chatLine) []string {
	var out []string
	for _, line := range lines {
		if line.Kind == lineAssistant {
			out = append(out, line.Text)
		}
	}
	return out
}

func toolLines(lines []chatLine) []string {
	var out []string
	for _, line := range lines {
		if line.Kind == lineTool {
			out = append(out, line.Text)
		}
	}
	return out
}

func reasoningLines(lines []chatLine) []string {
	var out []string
	for _, line := range lines {
		if line.Kind == lineReasoning {
			out = append(out, line.Text)
		}
	}
	return out
}

func detailLines(lines []chatLine) []string {
	var out []string
	for _, line := range lines {
		if line.Kind == lineDetail {
			out = append(out, line.Text)
		}
	}
	return out
}
