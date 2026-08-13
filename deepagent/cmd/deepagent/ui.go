package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/constant"
	execmw "eino-cli/deepagent/core/middleware/execute"
	"eino-cli/deepagent/core/middleware/planmode"
	"eino-cli/deepagent/worker"
	inprocess "eino-cli/deepagent/worker/inprocess"
	"eino-cli/deepagent/worker/tasktool"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const maxChatLines = 500

var (
	userStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(lipgloss.Color("#2563eb")).Padding(0, 1)
	textStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f6fc"))
	eventStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e"))
	reasoningStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#7d8590")).Italic(true)
	toolStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("#f2cc60"))
	detailStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#7d8590"))
	errorStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff7b72"))
	liveDraftStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9")).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#3fb950")).Padding(0, 1)
	panelStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f6fc")).Background(lipgloss.Color("#30363d")).Padding(1, 2)
	statusStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#7d8590"))
	activeStatusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9"))
	alertStatusStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#f2cc60"))

	transcriptAssistantBulletStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6e7681"))
	transcriptAssistantStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9"))
	transcriptReasoningStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#7d8590")).Italic(true)
	transcriptToolStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("#f2cc60")).Bold(true)
	transcriptDetailStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e"))
	transcriptEventStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e"))
	transcriptErrorStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff7b72")).Bold(true)
)

type lineKind string

const (
	lineSystem    lineKind = "system"
	lineUser      lineKind = "user"
	lineAssistant lineKind = "assistant"
	lineReasoning lineKind = "reasoning"
	lineEvent     lineKind = "event"
	lineTool      lineKind = "tool"
	lineDetail    lineKind = "detail"
	lineError     lineKind = "error"
)

type chatLine struct {
	Kind lineKind
	Text string
}

type pendingApproval struct {
	Key             string
	ThreadID        string
	TurnID          string
	CheckpointID    string
	InterruptID     string
	ToolName        string
	ArgumentsInJSON string
}

type pendingPlanInput struct {
	Key            string
	ThreadID       string
	TurnID         string
	CheckpointID   string
	InterruptID    string
	Questions      []planmode.Question
	ActiveQuestion int
	Selected       []int
	Answers        []string
	Answered       []bool
	Drafts         []string
}

type pendingPlanAction struct {
	ThreadID string
	TurnID   string
	Plan     string
	Selected int
}

func (p *pendingPlanInput) active() planmode.Question {
	if p == nil || len(p.Questions) == 0 {
		return planmode.Question{}
	}
	if p.ActiveQuestion < 0 {
		p.ActiveQuestion = 0
	}
	if p.ActiveQuestion >= len(p.Questions) {
		p.ActiveQuestion = len(p.Questions) - 1
	}
	return p.Questions[p.ActiveQuestion]
}

func (p *pendingPlanInput) selectedForActive() int {
	if p == nil {
		return 0
	}
	p.ensureSelected()
	if len(p.Selected) == 0 {
		return 0
	}
	return p.Selected[p.ActiveQuestion]
}

func (p *pendingPlanInput) ensureSelected() {
	if p == nil {
		return
	}
	if len(p.Selected) != len(p.Questions) {
		next := make([]int, len(p.Questions))
		copy(next, p.Selected)
		p.Selected = next
	}
	if len(p.Answers) != len(p.Questions) {
		next := make([]string, len(p.Questions))
		copy(next, p.Answers)
		p.Answers = next
	}
	if len(p.Answered) != len(p.Questions) {
		next := make([]bool, len(p.Questions))
		copy(next, p.Answered)
		p.Answered = next
	}
	if len(p.Drafts) != len(p.Questions) {
		next := make([]string, len(p.Questions))
		copy(next, p.Drafts)
		p.Drafts = next
	}
	if len(p.Questions) == 0 {
		p.ActiveQuestion = 0
		return
	}
	if p.ActiveQuestion < 0 {
		p.ActiveQuestion = 0
	}
	if p.ActiveQuestion >= len(p.Questions) {
		p.ActiveQuestion = len(p.Questions) - 1
	}
	for i, q := range p.Questions {
		if len(q.Options) == 0 {
			p.Selected[i] = 0
			continue
		}
		if p.Selected[i] < 0 {
			p.Selected[i] = 0
		}
		if p.Selected[i] >= len(q.Options) {
			p.Selected[i] = len(q.Options) - 1
		}
	}
}

type assistantDraft struct {
	Text                 string
	ReasoningText        string
	VisibleText          string
	VisibleReasoningText string
	HadToken             bool
	LastVisibleAt        time.Time
}

type threadViewState struct {
	ThreadID       string
	Role           string
	Title          string
	ParentThreadID string
	RootThreadID   string
	PendingBlock   *agentworker.PendingBlock
	LastEventType  agentworker.EventType
	Activity       string
	ToolName       string
}

type chatModel struct {
	ctx             context.Context
	service         *LocalAgentService
	initialThreadID string
	autoResume      bool

	width    int
	height   int
	input    string
	planMode bool

	active       *inprocess.ThreadState
	threads      []*inprocess.ThreadState
	lines        []chatLine
	printQueue   []string
	busy         bool
	scrollOffset int

	statusLineFields map[string]bool
	contextUsed      int
	contextWindow    int

	seenEvents       map[string]struct{}
	assistantDrafts  map[string]*assistantDraft
	assistantFinals  map[string]struct{}
	threadViews      map[string]*threadViewState
	pendingApprovals map[string]*pendingApproval

	resumeMode  bool
	resumeList  []*inprocess.ThreadState
	resumeIndex int
	pending     *pendingApproval
	planPending *pendingPlanInput
	planAction  *pendingPlanAction
}

type bindMsg struct {
	binding *ThreadBinding
	err     error
}

type sendMsg struct {
	content   string
	messageID string
	planMode  bool
	err       error
}

type planInputMsg struct {
	pending  pendingPlanInput
	response *planmode.RequestUserInputResponse
	err      error
}

type updateMsg struct {
	update localUpdate
}

type rootsMsg struct {
	roots []*inprocess.ThreadState
	err   error
}

type threadsMsg struct {
	threads []*inprocess.ThreadState
	err     error
}

type stopMsg struct {
	result *inprocess.InterruptThreadResult
	err    error
}

type approvalMsg struct {
	pending  pendingApproval
	decision localApprovalDecision
	err      error
}

var statusLineFieldOrder = []string{"status", "uid", "context", "session", "active", "cwd", "threads"}

func newChatModel(ctx context.Context, service *LocalAgentService) chatModel {
	m := chatModel{
		ctx:              ctx,
		service:          service,
		statusLineFields: defaultStatusLineFields(),
		contextWindow:    defaultStatusLineContextWindow(ctx),
		seenEvents:       make(map[string]struct{}),
		assistantDrafts:  make(map[string]*assistantDraft),
		assistantFinals:  make(map[string]struct{}),
		threadViews:      make(map[string]*threadViewState),
		pendingApprovals: make(map[string]*pendingApproval),
	}
	m.append(lineSystem, "DeepAgent local chat. Type /help for commands.")
	return m
}

func (m chatModel) Init() tea.Cmd {
	cmds := []tea.Cmd{waitLocalUpdate(m.service)}
	if m.initialThreadID != "" {
		cmds = append(cmds, bindThreadCmd(m.ctx, m.service, m.initialThreadID))
	} else if m.autoResume {
		cmds = append(cmds, autoResumeLatestCmd(m.ctx, m.service))
	}
	return tea.Batch(cmds...)
}

func (m chatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch x := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = x.Width
		m.height = x.Height
	case tea.KeyMsg:
		next, cmd := m.handleKey(x)
		if typed, ok := next.(chatModel); ok {
			return typed.withPrint(cmd)
		}
		return next, cmd
	case bindMsg:
		m.busy = false
		if x.err != nil {
			m.append(lineError, x.err.Error())
			break
		}
		m.applyBinding(x.binding)
	case sendMsg:
		if x.err != nil {
			m.busy = false
			m.append(lineError, x.err.Error())
		}
	case updateMsg:
		m.applyUpdate(x.update)
		return m.withPrint(waitLocalUpdate(m.service))
	case rootsMsg:
		m.busy = false
		if x.err != nil {
			m.append(lineError, x.err.Error())
			break
		}
		if len(x.roots) == 0 {
			m.resumeMode = false
			m.resumeList = nil
			m.resumeIndex = 0
			m.append(lineSystem, "no resumable sessions for this user/workdir")
			break
		}
		m.resumeMode = true
		m.resumeList = x.roots
		m.resumeIndex = 0
	case threadsMsg:
		if x.err != nil {
			m.append(lineError, x.err.Error())
			break
		}
		m.threads = x.threads
		m.rememberThreads(x.threads)
		if m.active != nil {
			if err := m.service.WatchSession(m.ctx, m.active.SessionID); err != nil {
				m.append(lineError, err.Error())
			}
		}
		m.appendKnownThreads()
	case stopMsg:
		if x.err != nil {
			m.append(lineError, x.err.Error())
			break
		}
		status := "unknown"
		if x.result != nil {
			status = string(x.result.Status)
		}
		m.append(lineEvent, "stop requested: "+status)
	case approvalMsg:
		m.busy = false
		if x.err != nil {
			m.append(lineError, x.err.Error())
			m.syncActivePendingApproval()
			break
		}
		m.clearPendingApproval(x.pending)
		if x.decision.Approved && x.decision.AllowInSession {
			m.append(lineEvent, "approval accepted for this session")
		} else if x.decision.Approved {
			m.append(lineEvent, "approval accepted")
		} else {
			m.append(lineEvent, "approval rejected")
		}
	case planInputMsg:
		m.busy = false
		if x.err != nil {
			m.append(lineError, x.err.Error())
			break
		}
		if m.planPending != nil && m.planPending.Key == x.pending.Key {
			m.planPending = nil
		}
		m.append(lineEvent, "plan input submitted")
	}
	return m.withPrint(nil)
}

func (m chatModel) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	if m.planPending != nil {
		switch key.String() {
		case "up", "k":
			m.movePlanSelection(-1)
			return m, nil
		case "down", "j":
			m.movePlanSelection(1)
			return m, nil
		case "left", "h":
			m.movePlanQuestion(-1)
			return m, nil
		case "right", "l", "tab":
			m.movePlanQuestion(1)
			return m, nil
		case "esc":
			m.input = ""
			return m, nil
		case "backspace", "ctrl+h":
			m.input = dropLastRune(m.input)
			return m, nil
		case "enter":
			if err := m.commitActivePlanAnswer(); err != nil {
				m.append(lineError, err.Error())
				return m, nil
			}
			if m.planPending.ActiveQuestion+1 < len(m.planPending.Questions) {
				m.planPending.ActiveQuestion++
				m.input = m.planPending.Drafts[m.planPending.ActiveQuestion]
				return m, nil
			}
			response, err := buildPlanInputResponse(*m.planPending)
			if err != nil {
				m.append(lineError, err.Error())
				return m, nil
			}
			pending := *m.planPending
			m.input = ""
			m.busy = true
			return m, submitPlanInputCmd(m.ctx, m.service, pending, response)
		default:
			if len(key.Runes) > 0 {
				if len(key.Runes) == 1 && key.Runes[0] >= '1' && key.Runes[0] <= '9' && strings.TrimSpace(m.input) == "" {
					m.setPlanSelection(int(key.Runes[0] - '1'))
					return m, nil
				}
				m.input += string(key.Runes)
			}
			return m, nil
		}
	}
	if m.planAction != nil {
		switch key.String() {
		case "up", "k", "down", "j", "tab":
			if m.planAction.Selected == 0 {
				m.planAction.Selected = 1
			} else {
				m.planAction.Selected = 0
			}
			return m, nil
		case "esc":
			m.planAction = nil
			return m, nil
		case "enter":
			action := *m.planAction
			m.planAction = nil
			if action.Selected == 0 {
				m.planMode = false
				content := "Implement the plan."
				m.append(lineUser, content)
				m.busy = true
				if m.active == nil {
					return m, startNewCmd(m.ctx, m.service, content, nil, false)
				}
				return m, sendUserCmd(m.ctx, m.service, m.active.ID, content, nil, false)
			}
			m.planMode = true
			m.append(lineSystem, "PLAN mode: describe what should change in the plan.")
			return m, nil
		}
	}
	if key.String() == "shift+tab" {
		m.planMode = !m.planMode
		return m, nil
	}
	if m.pending != nil {
		switch key.String() {
		case "y":
			decision := localApprovalDecision{Approved: true}
			m.busy = true
			return m, approveCmd(m.ctx, m.service, *m.pending, decision)
		case "p":
			decision := localApprovalDecision{Approved: true, AllowInSession: true}
			m.busy = true
			return m, approveCmd(m.ctx, m.service, *m.pending, decision)
		case "n", "esc":
			decision := localApprovalDecision{Approved: false, Reason: "rejected by user"}
			m.busy = true
			return m, approveCmd(m.ctx, m.service, *m.pending, decision)
		}
		return m, nil
	}
	if m.resumeMode {
		switch key.String() {
		case "esc":
			m.resumeMode = false
			m.input = ""
			m.resumeIndex = 0
			return m, nil
		case "up", "k":
			if m.resumeIndex > 0 {
				m.resumeIndex--
			}
			return m, nil
		case "down", "j":
			if m.resumeIndex < len(m.resumeList)-1 {
				m.resumeIndex++
			}
			return m, nil
		case "enter":
			idx := m.resumeIndex
			ok := idx >= 0 && idx < len(m.resumeList)
			if strings.TrimSpace(m.input) != "" {
				idx, ok = parseResumeChoice(m.input, len(m.resumeList))
			}
			m.input = ""
			if !ok {
				m.append(lineError, "enter a valid resume number")
				return m, nil
			}
			m.resumeMode = false
			m.resumeIndex = 0
			m.busy = true
			return m, bindThreadCmd(m.ctx, m.service, m.resumeList[idx].ID)
		}
	}
	switch key.String() {
	case "ctrl+c", "esc":
		return m, tea.Quit
	case "pgup":
		m.scrollBy(m.bodyViewportHeight())
	case "pgdown":
		m.scrollBy(-m.bodyViewportHeight())
	case "home":
		m.scrollToTop()
	case "end":
		m.scrollToBottom()
	case "backspace", "ctrl+h":
		m.input = dropLastRune(m.input)
	case "enter":
		input := strings.TrimSpace(m.input)
		m.input = ""
		if input == "" {
			return m, nil
		}
		return m.submit(input)
	default:
		if len(key.Runes) > 0 {
			m.input += string(key.Runes)
		}
	}
	return m, nil
}

func (m chatModel) submit(input string) (tea.Model, tea.Cmd) {
	if strings.HasPrefix(input, "/") {
		return m.handleCommand(input)
	}
	m.append(lineUser, input)
	m.busy = true
	planMode := m.planMode
	metadata := turnModeMetadata(planMode)
	if m.active == nil {
		return m, startNewCmd(m.ctx, m.service, input, metadata, planMode)
	}
	return m, sendUserCmd(m.ctx, m.service, m.active.ID, input, metadata, planMode)
}

func (m chatModel) handleCommand(input string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(input)
	cmd := fields[0]
	switch cmd {
	case "/help":
		m.append(lineSystem, "/new, /resume, /threads, /switch <thread_id>, /stop, /statusline [show|hide|toggle|reset] [field...], /exit")
	case "/exit", "/quit":
		return m, tea.Quit
	case "/new":
		m.active = nil
		m.threads = nil
		m.pending = nil
		m.pendingApprovals = make(map[string]*pendingApproval)
		m.threadViews = make(map[string]*threadViewState)
		m.assistantDrafts = make(map[string]*assistantDraft)
		m.append(lineSystem, "new session will be created on next message")
	case "/resume":
		m.busy = true
		return m, listRootsCmd(m.ctx, m.service)
	case "/threads":
		if m.active == nil {
			m.append(lineSystem, "no active session")
			break
		}
		return m, listThreadsCmd(m.ctx, m.service, m.active.SessionID)
	case "/switch":
		if len(fields) < 2 {
			m.append(lineError, "usage: /switch <thread>")
			break
		}
		m.busy = true
		sessionID := ""
		if m.active != nil {
			sessionID = m.active.SessionID
		}
		return m, switchThreadCmd(m.ctx, m.service, sessionID, fields[1])
	case "/stop":
		if m.active == nil {
			m.append(lineSystem, "no active session")
			break
		}
		return m, stopThreadCmd(m.ctx, m.service, m.active.ID)
	case "/statusline":
		m.handleStatusLineCommand(fields[1:])
	default:
		m.append(lineError, "unknown command: "+cmd)
	}
	return m, nil
}

func (m *chatModel) handleStatusLineCommand(args []string) {
	if len(args) == 0 {
		m.append(lineSystem, m.statusLineHelp())
		return
	}
	action := args[0]
	if action == "reset" {
		m.statusLineFields = defaultStatusLineFields()
		m.append(lineSystem, "statusline reset: "+m.enabledStatusLineFields())
		return
	}
	if action != "show" && action != "hide" && action != "toggle" {
		m.append(lineError, "usage: /statusline [show|hide|toggle|reset] [status|uid|context|session|active|cwd|threads]")
		return
	}
	if len(args) == 1 {
		m.append(lineError, "missing statusline field")
		return
	}
	for _, field := range args[1:] {
		if !validStatusLineField(field) {
			m.append(lineError, "unknown statusline field: "+field)
			return
		}
		switch action {
		case "show":
			m.statusLineFields[field] = true
		case "hide":
			m.statusLineFields[field] = false
		case "toggle":
			m.statusLineFields[field] = !m.statusLineFields[field]
		}
	}
	m.append(lineSystem, "statusline: "+m.enabledStatusLineFields())
}

func (m *chatModel) applyBinding(binding *ThreadBinding) {
	if binding == nil || binding.Thread == nil {
		return
	}
	m.active = binding.Thread
	m.threads = binding.Threads
	if err := m.service.WatchSession(m.ctx, binding.Thread.SessionID); err != nil {
		m.append(lineError, err.Error())
	}
	m.seenEvents = make(map[string]struct{})
	m.assistantDrafts = make(map[string]*assistantDraft)
	m.assistantFinals = make(map[string]struct{})
	m.pendingApprovals = make(map[string]*pendingApproval)
	m.pending = nil
	m.planPending = nil
	m.threadViews = make(map[string]*threadViewState)
	m.lines = nil
	m.printQueue = nil
	m.rememberThreads(binding.Threads)

	m.append(lineSystem, fmt.Sprintf("opened session=%s main=%s", binding.Thread.SessionID, binding.Thread.ID))
	for _, ev := range binding.Events {
		m.markEventSeen(ev)
	}
	m.applyHistoricalEvents(binding.Events)
	for _, ev := range binding.SidecarEvents {
		m.applySidecarEvent(ev, true)
	}
	m.syncActivePendingApproval()
}

func (m *chatModel) applyUpdate(update localUpdate) {
	if update.Thread != nil {
		m.upsertThread(update.Thread)
	}
	if update.Event != nil {
		m.applyRealtimeEvent(update.Event)
	}
}

func (m *chatModel) upsertThread(thread *inprocess.ThreadState) {
	if thread == nil {
		return
	}
	for i, existing := range m.threads {
		if existing != nil && existing.ID == thread.ID {
			m.threads[i] = thread
			m.rememberThread(thread)
			return
		}
	}
	m.threads = append(m.threads, thread)
	m.rememberThread(thread)
}

func (m *chatModel) applyHistoricalEvent(ev *agentworker.Event) {
	if !m.markEventSeen(ev) {
		return
	}
	m.applyHistoricalEvents([]*agentworker.Event{ev})
}

func (m *chatModel) applyHistoricalEvents(events []*agentworker.Event) {
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if !m.isActiveThreadEvent(ev) {
			m.applySidecarEvent(ev, true)
			continue
		}
		m.applyResumeEvent(ev)
	}
	m.syncActivePendingApproval()
}

func (m *chatModel) applyResumeEvent(ev *agentworker.Event) {
	if ev == nil {
		return
	}
	payload := decodeLocalEventPayload(ev.Payload)
	m.observeContextUsage(payload)
	m.projectResumeThreadState(ev, payload)
	switch ev.Type {
	case agentworker.EventType(agentthread.EventTurnStart):
		if strings.TrimSpace(payload.Message) != "" {
			m.append(lineUser, payload.Message)
		}
	case agentworker.EventType(agentthread.EventLLMEnd):
		if strings.TrimSpace(payload.Message) != "" || strings.TrimSpace(payload.ReasoningText) != "" {
			m.appendAssistantSegment(payload.Message, payload.ReasoningText, false)
		}
	case agentworker.EventType(agentthread.EventToolEnd):
		if payload.Name == "update_plan" {
			m.appendUpdatedPlan(payload.ArgumentsInJSON)
			return
		}
		if payload.Name == tasktool.ToolWaitMessage {
			if result := formatWaitMessageResult(payload.Result); result != "" {
				m.append(lineDetail, result)
			}
			return
		}
		m.append(lineTool, formatToolEndFallbackSummary(payload.Name, payload.ArgumentsInJSON))
		if result := m.formatToolResultDetail(payload.Name, payload.ArgumentsInJSON, payload.Result); result != "" {
			m.append(lineDetail, result)
		}
	case agentworker.EventType(agentthread.EventApproveRequested):
		if !m.shouldApplyApprovalEvent(ev, payload, true) {
			return
		}
		pending := m.addPendingApproval(ev, payload)
		if pending.ThreadID == m.activeThreadID() {
			m.pending = pending
			m.append(lineEvent, fmt.Sprintf("%s waiting for approval: %s", shortThread(ev.ThreadID), firstNonEmpty(payload.ToolName, "unknown")))
		}
	case agentworker.EventType(agentthread.EventInterrupted):
		if payload.RequestUserInput == nil {
			return
		}
		pending := m.addPendingPlanInput(ev, payload)
		if pending.ThreadID == m.activeThreadID() {
			m.planPending = pending
			m.busy = false
			m.append(lineEvent, shortThread(ev.ThreadID)+" waiting for plan input")
		}
	case agentworker.EventType(agentthread.EventError):
		m.append(lineError, payload.Message)
	}
}

func (m *chatModel) projectResumeThreadState(ev *agentworker.Event, payload localEventPayload) {
	if ev == nil || ev.ThreadID == "" {
		return
	}
	view := m.threadViews[ev.ThreadID]
	if view == nil {
		view = &threadViewState{ThreadID: ev.ThreadID, Role: "thread"}
		m.threadViews[ev.ThreadID] = view
	}
	view.LastEventType = ev.Type
	switch ev.Type {
	case agentworker.EventType(agentthread.EventApproveRequested):
		if m.shouldApplyApprovalEvent(ev, payload, true) {
			view.Activity = "blocked"
			view.ToolName = payload.ToolName
		}
	case agentworker.EventType(agentthread.EventInterrupted):
		if payload.RequestUserInput != nil {
			view.Activity = "blocked"
			view.ToolName = "request_user_input"
		}
	case agentworker.EventType(agentthread.EventError):
		m.clearPendingForTurn(ev.ThreadID, ev.TurnID)
		view.Activity = "error"
		view.ToolName = ""
	case agentworker.EventType(agentthread.EventTurnEnd):
		m.clearPendingForTurn(ev.ThreadID, ev.TurnID)
		view.Activity = "idle"
		view.ToolName = ""
	case agentworker.EventType(agentthread.EventToolEnd):
		m.clearPendingForTurn(ev.ThreadID, ev.TurnID)
		if view.Activity != "blocked" {
			view.Activity = "running"
		}
	case agentworker.EventType(agentthread.EventLLMEnd):
		if view.Activity != "blocked" {
			view.Activity = "running"
		}
	}
}

func (m *chatModel) applyRealtimeEvent(ev *agentworker.Event) {
	if !m.markEventSeen(ev) {
		return
	}
	if m.isActiveThreadEvent(ev) {
		m.applyActiveEvent(ev, false)
		return
	}
	m.applySidecarEvent(ev, false)
}

func (m *chatModel) applySidecarEvent(ev *agentworker.Event, historical bool) {
	if ev == nil {
		return
	}
	payload := decodeLocalEventPayload(ev.Payload)
	m.updateThreadViewFromEvent(ev, payload, historical)
	if ev.Type == agentworker.EventType(agentthread.EventToolEnd) && payload.Name == tasktool.ToolSpawnTask {
		m.append(lineEvent, shortThread(ev.ThreadID)+" started a child task")
	}
}

func (m *chatModel) appendUpdatedPlan(args string) {
	detail := formatUpdatedPlanDetail(args)
	if detail == "" {
		return
	}
	m.append(lineTool, "Updated Plan")
	m.append(lineDetail, detail)
}

func (m *chatModel) formatToolResultDetail(name, args, raw string) string {
	return formatToolResultDetailWithWorkDir(name, args, raw, m.currentWorkDir())
}

func (m *chatModel) currentWorkDir() string {
	if m != nil && m.active != nil && strings.TrimSpace(m.active.Profile.Cwd) != "" {
		return strings.TrimSpace(m.active.Profile.Cwd)
	}
	if m != nil && m.service != nil && strings.TrimSpace(m.service.cfg.WorkDir) != "" {
		return strings.TrimSpace(m.service.cfg.WorkDir)
	}
	return ""
}

func (m *chatModel) applyActiveEvent(ev *agentworker.Event, historical bool) {
	if ev == nil {
		return
	}
	payload := decodeLocalEventPayload(ev.Payload)
	m.observeContextUsage(payload)
	m.updateThreadViewFromEvent(ev, payload, historical)
	switch ev.Type {
	case agentworker.EventType(agentthread.EventTurnStart):
		if historical && strings.TrimSpace(payload.Message) != "" {
			m.append(lineUser, payload.Message)
		}
		if !historical {
			m.busy = true
		}
	case agentworker.EventType(agentthread.EventLLMToken):
		if !historical {
			draft := m.assistantDraft(ev.ThreadID)
			draft.Text += payload.Text
			draft.ReasoningText += payload.ReasoningText
			if payload.Text != "" || payload.ReasoningText != "" {
				draft.HadToken = true
			}
			draft.maybeRefreshVisible(false)
		}
	case agentworker.EventType(agentthread.EventLLMEnd):
		if historical {
			m.appendAssistantFinal(ev.ThreadID, ev.TurnID, payload.Message, payload.ReasoningText, false)
			break
		}
		draft := m.assistantDraft(ev.ThreadID)
		if !draft.HadToken && (strings.TrimSpace(payload.Message) != "" || strings.TrimSpace(payload.ReasoningText) != "") {
			draft.Text += payload.Message
			draft.ReasoningText += payload.ReasoningText
			draft.HadToken = true
		} else if strings.TrimSpace(payload.Message) != "" && strings.TrimSpace(draft.Text) == "" {
			draft.Text += payload.Message
		}
		draft.maybeRefreshVisible(true)
	case agentworker.EventType(agentthread.EventToolStart):
		m.discardAssistant(ev.ThreadID)
		if payload.Name != "update_plan" {
			m.append(lineTool, formatToolStartSummary(payload.Name, payload.Args))
		}
	case agentworker.EventType(agentthread.EventToolEnd):
		m.clearPendingForTurn(ev.ThreadID, ev.TurnID)
		if payload.Name == "update_plan" {
			m.appendUpdatedPlan(payload.ArgumentsInJSON)
			return
		}
		if result := m.formatToolResultDetail(payload.Name, payload.ArgumentsInJSON, payload.Result); result != "" {
			m.append(lineDetail, result)
		}
	case agentworker.EventType(agentthread.EventApproveRequested):
		m.flushAssistant(ev.ThreadID)
		pending := m.addPendingApproval(ev, payload)
		if pending.ThreadID == m.activeThreadID() {
			m.pending = pending
			m.busy = false
			m.append(lineEvent, fmt.Sprintf("%s waiting for approval: %s", shortThread(ev.ThreadID), firstNonEmpty(payload.ToolName, "unknown")))
		}
	case agentworker.EventType(agentthread.EventInterrupted):
		if payload.RequestUserInput == nil {
			break
		}
		m.flushAssistant(ev.ThreadID)
		pending := m.addPendingPlanInput(ev, payload)
		if pending.ThreadID == m.activeThreadID() {
			m.planPending = pending
			m.busy = false
			m.append(lineEvent, shortThread(ev.ThreadID)+" waiting for plan input")
		}
	case agentworker.EventType(agentthread.EventError):
		m.flushAssistant(ev.ThreadID)
		m.busy = false
		m.append(lineError, payload.Message)
	case agentworker.EventType(agentthread.EventTurnEnd):
		m.clearPendingForTurn(ev.ThreadID, ev.TurnID)
		m.flushAssistant(ev.ThreadID)
		m.busy = false
	}
	m.syncActivePendingApproval()
}

func (m *chatModel) markEventSeen(ev *agentworker.Event) bool {
	if ev == nil || ev.ID == "" {
		return true
	}
	if _, ok := m.seenEvents[ev.ID]; ok {
		return false
	}
	m.seenEvents[ev.ID] = struct{}{}
	return true
}

func (m *chatModel) isActiveThreadEvent(ev *agentworker.Event) bool {
	if ev == nil || ev.ThreadID == "" {
		return true
	}
	return ev.ThreadID == m.activeThreadID()
}

func (m *chatModel) activeThreadID() string {
	if m.active == nil {
		return ""
	}
	return m.active.ID
}

func (m *chatModel) assistantDraft(threadID string) *assistantDraft {
	if threadID == "" {
		threadID = m.activeThreadID()
	}
	draft := m.assistantDrafts[threadID]
	if draft == nil {
		draft = &assistantDraft{}
		m.assistantDrafts[threadID] = draft
	}
	return draft
}

func (d *assistantDraft) maybeRefreshVisible(force bool) {
	if d == nil {
		return
	}
	now := time.Now()
	if force || d.LastVisibleAt.IsZero() || now.Sub(d.LastVisibleAt) >= 80*time.Millisecond || strings.Contains(d.Text, "\n") || strings.Contains(d.ReasoningText, "\n") {
		d.VisibleText = d.Text
		d.VisibleReasoningText = d.ReasoningText
		d.LastVisibleAt = now
	}
}

func (m *chatModel) flushAssistant(threadID string) {
	draft := m.assistantDrafts[threadID]
	if draft == nil {
		return
	}
	text := strings.TrimRight(draft.Text, " \t\r\n")
	delete(m.assistantDrafts, threadID)
	if threadID != m.activeThreadID() {
		return
	}
	if strings.TrimSpace(text) != "" {
		m.append(lineAssistant, text)
		m.maybeOfferPlanAction(threadID, "", text)
	}
}

func (m *chatModel) discardAssistant(threadID string) {
	delete(m.assistantDrafts, threadID)
}

func (m *chatModel) appendAssistantFinal(threadID, turnID, text string, reasoning string, offerPlanAction bool) {
	if threadID != m.activeThreadID() {
		return
	}
	key := threadID + ":" + turnID
	if _, ok := m.assistantFinals[key]; ok {
		return
	}
	m.assistantFinals[key] = struct{}{}
	if strings.TrimSpace(text) != "" {
		m.append(lineAssistant, text)
		if offerPlanAction {
			m.maybeOfferPlanAction(threadID, turnID, text)
		}
	}
}

func (m *chatModel) appendAssistantSegment(text string, reasoning string, offerPlanAction bool) {
	if strings.TrimSpace(text) != "" {
		m.append(lineAssistant, text)
		if offerPlanAction {
			m.maybeOfferPlanAction(m.activeThreadID(), "", text)
		}
	}
}

func (m *chatModel) maybeOfferPlanAction(threadID, turnID, text string) {
	plan := extractProposedPlan(text)
	if strings.TrimSpace(plan) == "" {
		return
	}
	m.planAction = &pendingPlanAction{
		ThreadID: threadID,
		TurnID:   turnID,
		Plan:     plan,
	}
	m.busy = false
}

func (m *chatModel) rememberThreads(threads []*inprocess.ThreadState) {
	for _, thread := range threads {
		m.rememberThread(thread)
	}
}

func (m *chatModel) rememberThread(thread *inprocess.ThreadState) {
	if thread == nil {
		return
	}
	view := m.threadViews[thread.ID]
	if view == nil {
		view = &threadViewState{ThreadID: thread.ID}
		m.threadViews[thread.ID] = view
	}
	view.Title = thread.Title
	view.ParentThreadID = thread.ParentThreadID
	view.RootThreadID = thread.RootThreadID
	view.PendingBlock = clonePendingBlock(thread.PendingBlock)
	if thread.ParentThreadID == "" {
		view.Role = "main"
	} else {
		view.Role = "child"
	}
	if thread.PendingBlock != nil {
		view.Activity = "blocked"
	} else if view.Activity == "blocked" {
		view.Activity = ""
	}
}

func (m *chatModel) updateThreadViewFromEvent(ev *agentworker.Event, payload localEventPayload, historical bool) {
	if ev == nil || ev.ThreadID == "" {
		return
	}
	view := m.threadViews[ev.ThreadID]
	if view == nil {
		view = &threadViewState{ThreadID: ev.ThreadID, Role: "thread"}
		m.threadViews[ev.ThreadID] = view
	}
	view.LastEventType = ev.Type
	switch ev.Type {
	case agentworker.EventType(agentthread.EventTurnStart), agentworker.EventType(agentthread.EventLLMToken), agentworker.EventType(agentthread.EventLLMRequesting):
		if !historical {
			view.Activity = "thinking"
			view.ToolName = ""
		}
	case agentworker.EventType(agentthread.EventToolStart):
		if !historical {
			view.Activity = "tool"
			view.ToolName = payload.Name
		}
	case agentworker.EventType(agentthread.EventApproveRequested):
		if !m.shouldApplyApprovalEvent(ev, payload, historical) {
			return
		}
		view.Activity = "blocked"
		view.ToolName = payload.ToolName
		m.addPendingApproval(ev, payload)
	case agentworker.EventType(agentthread.EventInterrupted):
		if payload.RequestUserInput != nil {
			view.Activity = "blocked"
			view.ToolName = "request_user_input"
			m.addPendingPlanInput(ev, payload)
		}
	case agentworker.EventType(agentthread.EventError):
		view.Activity = "error"
		view.ToolName = ""
	case agentworker.EventType(agentthread.EventTurnEnd):
		m.clearPendingForTurn(ev.ThreadID, ev.TurnID)
		view.Activity = "idle"
		view.ToolName = ""
	case agentworker.EventType(agentthread.EventLLMEnd), agentworker.EventType(agentthread.EventToolEnd):
		if ev.Type == agentworker.EventType(agentthread.EventToolEnd) {
			m.clearPendingForTurn(ev.ThreadID, ev.TurnID)
		}
		if view.Activity != "blocked" {
			view.Activity = "running"
		}
	}
}

func (m *chatModel) shouldApplyApprovalEvent(ev *agentworker.Event, payload localEventPayload, historical bool) bool {
	if ev == nil {
		return false
	}
	if !historical {
		return true
	}
	block := m.pendingBlockForThread(ev.ThreadID)
	if block == nil {
		return false
	}
	if block.InterruptID != "" && payload.InterruptID != "" && block.InterruptID != payload.InterruptID {
		return false
	}
	if block.TurnID != "" && ev.TurnID != "" && block.TurnID != ev.TurnID {
		return false
	}
	return true
}

func (m *chatModel) pendingBlockForThread(threadID string) *agentworker.PendingBlock {
	if m.active != nil && m.active.ID == threadID {
		return m.active.PendingBlock
	}
	if view := m.threadViews[threadID]; view != nil {
		return view.PendingBlock
	}
	for _, thread := range m.threads {
		if thread != nil && thread.ID == threadID {
			return thread.PendingBlock
		}
	}
	return nil
}

func clonePendingBlock(in *agentworker.PendingBlock) *agentworker.PendingBlock {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func (m *chatModel) addPendingApproval(ev *agentworker.Event, payload localEventPayload) *pendingApproval {
	key := ev.ThreadID + ":" + ev.TurnID + ":" + payload.InterruptID
	pending := &pendingApproval{
		Key:             key,
		ThreadID:        ev.ThreadID,
		TurnID:          ev.TurnID,
		CheckpointID:    payload.CheckpointID,
		InterruptID:     payload.InterruptID,
		ToolName:        payload.ToolName,
		ArgumentsInJSON: payload.ArgumentsInJSON,
	}
	m.pendingApprovals[key] = pending
	return pending
}

func (m *chatModel) addPendingPlanInput(ev *agentworker.Event, payload localEventPayload) *pendingPlanInput {
	if payload.RequestUserInput == nil {
		return nil
	}
	key := ev.ThreadID + ":" + ev.TurnID + ":" + payload.InterruptID
	pending := &pendingPlanInput{
		Key:          key,
		ThreadID:     ev.ThreadID,
		TurnID:       ev.TurnID,
		CheckpointID: payload.CheckpointID,
		InterruptID:  payload.InterruptID,
		Questions:    append([]planmode.Question(nil), payload.RequestUserInput.Questions...),
	}
	pending.ensureSelected()
	return pending
}

func (m *chatModel) movePlanSelection(delta int) {
	if m == nil || m.planPending == nil || len(m.planPending.Questions) == 0 {
		return
	}
	p := m.planPending
	p.ensureSelected()
	q := p.Questions[p.ActiveQuestion]
	if len(q.Options) == 0 {
		return
	}
	p.Selected[p.ActiveQuestion] = (p.Selected[p.ActiveQuestion] + delta + len(q.Options)) % len(q.Options)
	p.Drafts[p.ActiveQuestion] = ""
	m.input = ""
}

func (m *chatModel) setPlanSelection(idx int) {
	if m == nil || m.planPending == nil || len(m.planPending.Questions) == 0 {
		return
	}
	p := m.planPending
	p.ensureSelected()
	q := p.Questions[p.ActiveQuestion]
	if idx < 0 || idx >= len(q.Options) {
		return
	}
	p.Selected[p.ActiveQuestion] = idx
	p.Drafts[p.ActiveQuestion] = ""
	m.input = ""
}

func (m *chatModel) movePlanQuestion(delta int) {
	if m == nil || m.planPending == nil || len(m.planPending.Questions) == 0 {
		return
	}
	p := m.planPending
	p.ensureSelected()
	p.Drafts[p.ActiveQuestion] = m.input
	p.ActiveQuestion = (p.ActiveQuestion + delta + len(p.Questions)) % len(p.Questions)
	m.input = p.Drafts[p.ActiveQuestion]
}

func (m *chatModel) commitActivePlanAnswer() error {
	if m == nil || m.planPending == nil || len(m.planPending.Questions) == 0 {
		return fmt.Errorf("plan input has no questions")
	}
	p := m.planPending
	p.ensureSelected()
	idx := p.ActiveQuestion
	q := p.Questions[idx]
	raw := strings.TrimSpace(m.input)
	answer := ""
	if raw != "" {
		answer = planAnswerForQuestion(q, raw)
		p.Drafts[idx] = raw
	} else if len(q.Options) > 0 {
		selected := p.Selected[idx]
		if selected < 0 || selected >= len(q.Options) {
			selected = 0
			p.Selected[idx] = selected
		}
		answer = q.Options[selected].Label
		p.Drafts[idx] = ""
	}
	if strings.TrimSpace(answer) == "" {
		return fmt.Errorf("empty answer for %s", firstNonEmpty(q.Header, q.ID))
	}
	p.Answers[idx] = answer
	p.Answered[idx] = true
	return nil
}

func (m *chatModel) clearPendingApproval(pending pendingApproval) {
	if pending.Key != "" {
		delete(m.pendingApprovals, pending.Key)
	}
	m.clearPendingForTurn(pending.ThreadID, pending.TurnID)
	if view := m.threadViews[pending.ThreadID]; view != nil && view.Activity == "blocked" {
		view.Activity = "running"
	}
	m.syncActivePendingApproval()
}

func (m *chatModel) clearPendingForTurn(threadID, turnID string) {
	if threadID == "" || turnID == "" {
		return
	}
	for key, pending := range m.pendingApprovals {
		if pending != nil && pending.ThreadID == threadID && pending.TurnID == turnID {
			delete(m.pendingApprovals, key)
		}
	}
	if m.planPending != nil && m.planPending.ThreadID == threadID && m.planPending.TurnID == turnID {
		m.planPending = nil
	}
}

func (m *chatModel) syncActivePendingApproval() {
	m.pending = nil
	activeID := m.activeThreadID()
	if activeID == "" {
		return
	}
	keys := make([]string, 0, len(m.pendingApprovals))
	for key, pending := range m.pendingApprovals {
		if pending != nil && pending.ThreadID == activeID {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		m.pending = m.pendingApprovals[keys[0]]
	}
}

func (m *chatModel) appendKnownThreads() {
	threads := m.sortedThreads()
	if len(threads) == 0 {
		m.append(lineEvent, "current session has no known threads")
		return
	}
	m.append(lineEvent, fmt.Sprintf("current session threads: count=%d", len(threads)))
	for _, thread := range threads {
		if thread == nil {
			continue
		}
		parts := []string{thread.Role, "thread=" + shortThread(thread.ThreadID)}
		if thread.ThreadID == m.activeThreadID() {
			parts = append(parts, "active")
		}
		if thread.Title != "" {
			parts = append(parts, "title="+truncateForChat(thread.Title, 50))
		}
		if status := threadStatus(thread); status != "" {
			parts = append(parts, "status="+status)
		}
		if thread.ParentThreadID != "" {
			parts = append(parts, "parent="+shortThread(thread.ParentThreadID))
		}
		m.append(lineDetail, strings.Join(parts, " "))
	}
}

func (m *chatModel) sortedThreads() []*threadViewState {
	out := make([]*threadViewState, 0, len(m.threadViews))
	for _, thread := range m.threadViews {
		out = append(out, thread)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ThreadID == m.activeThreadID() {
			return true
		}
		if out[j].ThreadID == m.activeThreadID() {
			return false
		}
		if out[i].Role != out[j].Role {
			return out[i].Role == "main"
		}
		return out[i].ThreadID < out[j].ThreadID
	})
	return out
}

func (m *chatModel) append(kind lineKind, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	wasAtBottom := m.scrollOffset == 0
	line := chatLine{Kind: kind, Text: text}
	m.lines = append(m.lines, line)
	if rendered := m.formatTranscriptLine(line); rendered != "" {
		m.printQueue = append(m.printQueue, rendered)
	}
	if len(m.lines) > maxChatLines {
		m.lines = m.lines[len(m.lines)-maxChatLines:]
	}
	if wasAtBottom {
		m.scrollOffset = 0
	} else {
		m.clampScrollOffset()
	}
}

func (m chatModel) withPrint(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	printCmd := m.printQueuedCmd()
	return m, tea.Batch(cmd, printCmd)
}

func (m *chatModel) printQueuedCmd() tea.Cmd {
	if len(m.printQueue) == 0 {
		return nil
	}
	lines := append([]string(nil), m.printQueue...)
	m.printQueue = nil
	return tea.Println(strings.Join(lines, "\n"))
}

func (m chatModel) View() string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	draft := m.renderActiveDraft(width)
	panel := m.renderPanel(width)
	statusBar := m.renderStatus(width)
	parts := make([]string, 0, 3)
	if strings.TrimSpace(draft) != "" {
		parts = append(parts, draft)
	}
	parts = append(parts, panel)
	if strings.TrimSpace(statusBar) != "" {
		parts = append(parts, statusBar)
	}
	return strings.Join(parts, "\n")
}

func (m chatModel) activeDraftLines() []chatLine {
	if m.active == nil {
		return nil
	}
	draft := m.assistantDrafts[m.active.ID]
	if draft == nil {
		return nil
	}
	lines := make([]chatLine, 0, 2)
	if text := strings.TrimRight(draft.Text, " \t\r\n"); strings.TrimSpace(text) != "" {
		lines = append(lines, chatLine{Kind: lineAssistant, Text: text})
	} else if reasoning := strings.TrimRight(draft.ReasoningText, " \t\r\n"); strings.TrimSpace(reasoning) != "" {
		lines = append(lines, chatLine{Kind: lineReasoning, Text: reasoning})
	}
	return lines
}

func (m chatModel) activeVisibleDraftLines() []chatLine {
	if m.active == nil {
		return nil
	}
	draft := m.assistantDrafts[m.active.ID]
	if draft == nil {
		return nil
	}
	lines := make([]chatLine, 0, 2)
	if text := strings.TrimRight(firstNonEmpty(draft.VisibleText, draft.Text), " \t\r\n"); strings.TrimSpace(text) != "" {
		lines = append(lines, chatLine{Kind: lineAssistant, Text: text})
	} else if reasoning := strings.TrimRight(firstNonEmpty(draft.VisibleReasoningText, draft.ReasoningText), " \t\r\n"); strings.TrimSpace(reasoning) != "" {
		lines = append(lines, chatLine{Kind: lineReasoning, Text: reasoning})
	}
	return lines
}

func (m chatModel) appendActiveDraftLines(lines []chatLine) []chatLine {
	return append(lines, m.activeDraftLines()...)
}

func (m chatModel) renderActiveDraft(width int) string {
	lines := m.activeVisibleDraftLines()
	if len(lines) == 0 {
		return ""
	}
	panelWidth := max(30, width-4)
	contentWidth := max(20, panelWidth-6)
	rendered := renderChatLines(lines, contentWidth)
	rendered = tailLines(rendered, 4)
	for len(rendered) < 4 {
		rendered = append(rendered, "")
	}
	content := transcriptAssistantBulletStyle.Render("• responding") + "\n" + strings.Join(rendered, "\n")
	return liveDraftStyle.Width(panelWidth).Render(content)
}

func (m chatModel) renderBody(width int, lines []chatLine) string {
	height := m.bodyViewportHeight()
	rendered := renderChatLines(lines, width)
	rendered = visibleBodyLines(rendered, height, m.scrollOffset)
	return strings.Join(rendered, "\n")
}

func (m *chatModel) scrollBy(delta int) {
	m.scrollOffset += delta
	m.clampScrollOffset()
}

func (m *chatModel) scrollToTop() {
	m.scrollOffset = m.maxScrollOffset()
}

func (m *chatModel) scrollToBottom() {
	m.scrollOffset = 0
}

func (m *chatModel) clampScrollOffset() {
	maxOffset := m.maxScrollOffset()
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
	if m.scrollOffset > maxOffset {
		m.scrollOffset = maxOffset
	}
}

func (m chatModel) maxScrollOffset() int {
	width := m.width
	if width <= 0 {
		width = 100
	}
	bodyLines := append([]chatLine{}, m.lines...)
	bodyLines = m.appendActiveDraftLines(bodyLines)
	rendered := renderChatLines(bodyLines, width)
	overflow := len(rendered) - m.bodyViewportHeight()
	if overflow < 0 {
		return 0
	}
	return overflow
}

func (m chatModel) bodyViewportHeight() int {
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := m.height
	if height <= 0 {
		height = 30
	}
	bodyHeight := height - renderedLineCount(m.renderPanel(width)) - renderedLineCount(m.renderStatus(width)) - 2
	if bodyHeight < 1 {
		return 1
	}
	return bodyHeight
}

func (m chatModel) renderPanel(width int) string {
	panelWidth := max(30, width-4)
	if m.planPending != nil {
		contentWidth := max(20, panelWidth-4)
		q := m.planPending.active()
		lines := []string{fmt.Sprintf("Plan input requested  %d/%d", m.planPending.ActiveQuestion+1, len(m.planPending.Questions)), ""}
		header := strings.TrimSpace(q.Header)
		if header == "" {
			header = fmt.Sprintf("Question %d", m.planPending.ActiveQuestion+1)
		}
		lines = append(lines, truncateForChat(header+": "+q.Question, contentWidth), "")
		selected := m.planPending.selectedForActive()
		for oi, opt := range q.Options {
			marker := "  "
			if oi == selected {
				marker = "> "
			}
			lines = append(lines, truncateForChat(fmt.Sprintf("%s%d. %s - %s", marker, oi+1, opt.Label, opt.Description), contentWidth))
		}
		if strings.TrimSpace(m.input) != "" {
			lines = append(lines, "", "custom: "+truncateForChat(m.input, contentWidth))
		} else if m.planPending.Answered[m.planPending.ActiveQuestion] {
			lines = append(lines, "", "answered: "+truncateForChat(m.planPending.Answers[m.planPending.ActiveQuestion], contentWidth))
		}
		enterHint := "Enter next"
		if m.planPending.ActiveQuestion+1 >= len(m.planPending.Questions) {
			enterHint = "Enter submit all"
		}
		lines = append(lines, "", "↑/↓ select  ←/→ question  "+enterHint)
		return panelStyle.Width(panelWidth).Render(strings.Join(lines, "\n"))
	}
	if m.planAction != nil {
		contentWidth := max(20, panelWidth-4)
		options := []string{"Yes, implement this plan", "No, stay in Plan mode"}
		lines := []string{"Implement this plan?", ""}
		for i, opt := range options {
			marker := "  "
			if i == m.planAction.Selected {
				marker = "> "
			}
			lines = append(lines, truncateForChat(marker+opt, contentWidth))
		}
		lines = append(lines, "", "↑/↓ select  Enter choose  Esc dismiss")
		return panelStyle.Width(panelWidth).Render(strings.Join(lines, "\n"))
	}
	if m.pending != nil {
		contentWidth := max(20, panelWidth-4)
		lines := []string{
			"Would you like to allow this tool call?",
			"",
			"tool: " + firstNonEmpty(m.pending.ToolName, "unknown"),
			"thread: " + shortThread(m.pending.ThreadID),
		}
		if m.pending.ArgumentsInJSON != "" {
			lines = append(lines, "args: "+truncateForChat(compactJSON(m.pending.ArgumentsInJSON), contentWidth))
		}
		lines = append(lines, "", "y: allow once    p: allow "+approvalSessionScopeLabel(m.pending)+" this session    n/esc: reject")
		return panelStyle.Width(panelWidth).Render(strings.Join(lines, "\n"))
	}
	if m.resumeMode {
		lines := []string{"Resume session: ↑/↓ select, Enter open, Esc cancel", ""}
		for i, thread := range m.resumeList {
			title := truncateForChat(firstNonEmpty(thread.Title, "untitled"), max(20, panelWidth-18))
			marker := "  "
			if i == m.resumeIndex {
				marker = "> "
			}
			lines = append(lines, fmt.Sprintf("%s%d. %s  %s", marker, i+1, shortThread(thread.ID), title))
			lines = append(lines, "    "+thread.UpdatedAt.Format("2006-01-02 15:04")+"  session="+shortThread(thread.SessionID))
		}
		return panelStyle.Width(panelWidth).Render(strings.Join(lines, "\n"))
	}
	if strings.HasPrefix(strings.TrimSpace(m.input), "/") {
		return panelStyle.Width(panelWidth).Render(strings.Join(m.commandHintLines(panelWidth), "\n"))
	}
	prefix := "> "
	if m.planMode {
		prefix = "PLAN> "
	}
	return panelStyle.Width(panelWidth).Render(prefix + m.input)
}

func (m chatModel) commandHintLines(width int) []string {
	input := strings.TrimSpace(m.input)
	lines := []string{"> " + m.input, ""}
	matches := matchingSlashCommands(input)
	if len(matches) == 0 {
		return append(lines, "no matching command")
	}
	lines = append(lines, "commands:")
	for _, cmd := range matches {
		lines = append(lines, "  "+truncateForChat(cmd.Name+"  "+cmd.Desc, max(20, width-6)))
	}
	return lines
}

type slashCommandHint struct {
	Name string
	Desc string
}

var slashCommandHints = []slashCommandHint{
	{Name: "/help", Desc: "show commands"},
	{Name: "/new", Desc: "start a new session on next message"},
	{Name: "/resume", Desc: "pick a previous session"},
	{Name: "/threads", Desc: "list threads in current session"},
	{Name: "/switch <thread>", Desc: "switch active thread by ref, id, or unique id prefix"},
	{Name: "/statusline", Desc: "configure bottom status line fields"},
	{Name: "/exit", Desc: "quit"},
}

func matchingSlashCommands(input string) []slashCommandHint {
	if input == "" || input == "/" {
		return slashCommandHints
	}
	out := make([]slashCommandHint, 0, len(slashCommandHints))
	for _, cmd := range slashCommandHints {
		name := strings.Fields(cmd.Name)[0]
		if strings.HasPrefix(cmd.Name, input) || strings.HasPrefix(name, strings.Fields(input)[0]) {
			out = append(out, cmd)
		}
	}
	return out
}

func (m chatModel) renderStatus(width int) string {
	runtimeStatus := m.runtimeStatus()
	segments := make([]string, 0, len(statusLineFieldOrder))
	if m.statusLineEnabled("status") {
		segments = append(segments, "status="+runtimeStatus)
	}
	if m.statusLineEnabled("uid") {
		uid := "?"
		if m.service != nil {
			uid = fmt.Sprintf("%d", m.service.cfg.UserID)
		}
		segments = append(segments, "uid="+uid)
	}
	if m.statusLineEnabled("context") {
		segments = append(segments, "ctx="+m.contextUsageLabel())
	}
	if m.statusLineEnabled("session") {
		session := "-"
		if m.active != nil {
			session = shortThread(m.active.SessionID)
		}
		segments = append(segments, "session="+session)
	}
	if m.statusLineEnabled("active") {
		active := "-"
		if m.active != nil {
			active = shortThread(m.active.ID)
		}
		segments = append(segments, "active="+active)
	}
	if m.statusLineEnabled("cwd") {
		cwd := "?"
		if m.service != nil {
			cwd = m.service.cfg.WorkDir
		}
		segments = append(segments, "cwd="+cwd)
	}

	lines := []string{}
	if len(segments) > 0 {
		lines = append(lines, statusStyle.Render(m.renderStatusLineWithMode(strings.Join(segments, "  "), width)))
	}
	threads := m.sortedThreads()
	if len(threads) == 0 || !m.statusLineEnabled("threads") {
		return strings.Join(lines, "\n")
	}
	parts := make([]string, 0, len(threads))
	for _, thread := range threads {
		label := thread.Role
		if thread.ThreadID == m.activeThreadID() {
			label += "*"
		}
		label += " " + shortThread(thread.ThreadID)
		if status := threadStatus(thread); status != "" {
			label += " " + status
		}
		if m.hasPendingApproval(thread.ThreadID) {
			parts = append(parts, alertStatusStyle.Render(label))
		} else if thread.ThreadID == m.activeThreadID() {
			parts = append(parts, activeStatusStyle.Render(label))
		} else {
			parts = append(parts, statusStyle.Render(label))
		}
	}
	lines = append(lines, statusStyle.Width(width).Render(strings.Join(parts, statusStyle.Render("   "))))
	return strings.Join(lines, "\n")
}

func (m chatModel) renderStatusLineWithMode(left string, width int) string {
	mode := "CODE"
	if m.planMode {
		mode = "PLAN"
	}
	if width <= 0 {
		return left + "  " + mode
	}
	left = truncateForChat(left, max(0, width-len(mode)-2))
	spaces := width - len(left) - len(mode)
	if spaces < 2 {
		spaces = 2
	}
	return left + strings.Repeat(" ", spaces) + mode
}

func (m chatModel) runtimeStatus() string {
	if m.planPending != nil {
		return "plan-input"
	}
	if m.planAction != nil {
		return "plan-ready"
	}
	if m.pending != nil {
		return "approval"
	}
	if active := m.threadViews[m.activeThreadID()]; active != nil {
		switch active.Activity {
		case "tool":
			if active.ToolName != "" {
				return "tool:" + active.ToolName
			}
			return "tool"
		case "thinking":
			return "thinking"
		case "running":
			return "running"
		case "blocked":
			return "blocked"
		case "error":
			return "error"
		}
	}
	if m.busy {
		return "busy"
	}
	return "ready"
}

func (m chatModel) statusLineEnabled(field string) bool {
	if m.statusLineFields == nil {
		return defaultStatusLineFields()[field]
	}
	return m.statusLineFields[field]
}

func (m chatModel) enabledStatusLineFields() string {
	fields := make([]string, 0, len(statusLineFieldOrder))
	for _, field := range statusLineFieldOrder {
		if m.statusLineEnabled(field) {
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 {
		return "(none)"
	}
	return strings.Join(fields, ", ")
}

func (m chatModel) statusLineHelp() string {
	return "statusline fields: " + m.enabledStatusLineFields() + "\nusage: /statusline show|hide|toggle status uid context session active cwd threads\nusage: /statusline reset"
}

func (m chatModel) contextUsageLabel() string {
	if m.contextUsed <= 0 {
		if m.contextWindow > 0 {
			return "-/" + formatTokenCount(m.contextWindow)
		}
		return "-"
	}
	if m.contextWindow <= 0 {
		return formatTokenCount(m.contextUsed)
	}
	pct := int(float64(m.contextUsed) / float64(m.contextWindow) * 100)
	return fmt.Sprintf("%s/%s %d%%", formatTokenCount(m.contextUsed), formatTokenCount(m.contextWindow), pct)
}

func (m *chatModel) observeContextUsage(payload localEventPayload) {
	if payload.TotalTokens > 0 {
		m.contextUsed = payload.TotalTokens
	}
}

func defaultStatusLineFields() map[string]bool {
	return map[string]bool{
		"status":  true,
		"uid":     true,
		"context": true,
		"session": true,
	}
}

func validStatusLineField(field string) bool {
	for _, candidate := range statusLineFieldOrder {
		if field == candidate {
			return true
		}
	}
	return false
}

func defaultStatusLineContextWindow(ctx context.Context) int {
	modelName := statusLineModelNameFromEnv()
	if modelName == "" {
		return 0
	}
	return constant.LookupModelContextWindow(ctx, modelName)
}

func statusLineModelNameFromEnv() string {
	if modelName := strings.TrimSpace(os.Getenv("MODEL_NAME")); modelName != "" {
		return modelName
	}
	for _, key := range []string{"OPENROUTER_MODEL", "KIMI_MODEL", "OPENAI_MODEL", "ARK_MODEL"} {
		if modelName := strings.TrimSpace(os.Getenv(key)); modelName != "" {
			return modelName
		}
	}
	switch {
	case strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) != "":
		return "anthropic/claude-sonnet-4.5"
	case strings.TrimSpace(os.Getenv("KIMI_API_KEY")) != "":
		return "kimi-k2.5"
	case strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")) != "":
		return "gpt-4o"
	default:
		return ""
	}
}

func formatTokenCount(n int) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fm", float64(n)/1000000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func (m chatModel) hasPendingApproval(threadID string) bool {
	for _, pending := range m.pendingApprovals {
		if pending != nil && pending.ThreadID == threadID {
			return true
		}
	}
	return false
}

func waitLocalUpdate(service *LocalAgentService) tea.Cmd {
	return func() tea.Msg {
		update := <-service.Updates()
		return updateMsg{update: update}
	}
}

func startNewCmd(ctx context.Context, service *LocalAgentService, content string, metadata map[string]string, planMode bool) tea.Cmd {
	return func() tea.Msg {
		thread, err := service.CreateRootThread(ctx, content)
		if err != nil {
			return bindMsg{err: err}
		}
		binding, err := service.BindThread(ctx, thread.ID)
		if err != nil {
			return bindMsg{err: err}
		}
		if _, err := service.SendUserMessage(ctx, thread.ID, content, metadata); err != nil {
			return bindMsg{err: err}
		}
		return bindMsg{binding: binding}
	}
}

func sendUserCmd(ctx context.Context, service *LocalAgentService, threadID, content string, metadata map[string]string, planMode bool) tea.Cmd {
	return func() tea.Msg {
		messageID, err := service.SendUserMessage(ctx, threadID, content, metadata)
		return sendMsg{content: content, messageID: messageID, planMode: planMode, err: err}
	}
}

func bindThreadCmd(ctx context.Context, service *LocalAgentService, threadID string) tea.Cmd {
	return func() tea.Msg {
		binding, err := service.BindThread(ctx, threadID)
		return bindMsg{binding: binding, err: err}
	}
}

func autoResumeLatestCmd(ctx context.Context, service *LocalAgentService) tea.Cmd {
	return func() tea.Msg {
		roots, err := service.ListResumableRoots(ctx)
		if err != nil {
			return bindMsg{err: err}
		}
		if len(roots) == 0 {
			return bindMsg{}
		}
		binding, err := service.BindThread(ctx, roots[0].ID)
		return bindMsg{binding: binding, err: err}
	}
}

func switchThreadCmd(ctx context.Context, service *LocalAgentService, sessionID, target string) tea.Cmd {
	return func() tea.Msg {
		threadID, err := service.ResolveThreadTarget(ctx, sessionID, target)
		if err != nil {
			return bindMsg{err: err}
		}
		binding, err := service.BindThread(ctx, threadID)
		return bindMsg{binding: binding, err: err}
	}
}

func listRootsCmd(ctx context.Context, service *LocalAgentService) tea.Cmd {
	return func() tea.Msg {
		roots, err := service.ListResumableRoots(ctx)
		return rootsMsg{roots: roots, err: err}
	}
}

func listThreadsCmd(ctx context.Context, service *LocalAgentService, sessionID string) tea.Cmd {
	return func() tea.Msg {
		threads, err := service.SessionThreads(ctx, sessionID)
		return threadsMsg{threads: threads, err: err}
	}
}

func stopThreadCmd(ctx context.Context, service *LocalAgentService, threadID string) tea.Cmd {
	return func() tea.Msg {
		result, err := service.CancelRunningThread(ctx, threadID, "user_stop")
		return stopMsg{result: result, err: err}
	}
}

func approveCmd(ctx context.Context, service *LocalAgentService, pending pendingApproval, decision localApprovalDecision) tea.Cmd {
	return func() tea.Msg {
		return approvalMsg{pending: pending, decision: decision, err: service.Approve(ctx, pending, decision)}
	}
}

func submitPlanInputCmd(ctx context.Context, service *LocalAgentService, pending pendingPlanInput, response *planmode.RequestUserInputResponse) tea.Cmd {
	return func() tea.Msg {
		return planInputMsg{pending: pending, response: response, err: service.SubmitPlanInput(ctx, pending, response)}
	}
}

func turnModeMetadata(planMode bool) map[string]string {
	if !planMode {
		return nil
	}
	return map[string]string{localTurnModeKey: localTurnModePlan}
}

func buildPlanInputResponse(pending pendingPlanInput) (*planmode.RequestUserInputResponse, error) {
	if len(pending.Questions) == 0 {
		return nil, fmt.Errorf("plan input has no questions")
	}
	answers := make(map[string]planmode.RequestUserInputAnswer, len(pending.Questions))
	pending.ensureSelected()
	for i, q := range pending.Questions {
		if !pending.Answered[i] {
			return nil, fmt.Errorf("question %d is not answered: %s", i+1, firstNonEmpty(q.Header, q.ID))
		}
		answer := pending.Answers[i]
		if strings.TrimSpace(answer) == "" {
			return nil, fmt.Errorf("empty answer for %s", firstNonEmpty(q.Header, q.ID))
		}
		answers[q.ID] = planmode.RequestUserInputAnswer{Answers: []string{answer}}
	}
	return &planmode.RequestUserInputResponse{Answers: answers}, nil
}

func planAnswerForQuestion(q planmode.Question, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var idx int
	if _, err := fmt.Sscanf(raw, "%d", &idx); err == nil && idx >= 1 && idx <= len(q.Options) {
		return q.Options[idx-1].Label
	}
	return raw
}

func extractProposedPlan(text string) string {
	const (
		openTag  = "<proposed_plan>"
		closeTag = "</proposed_plan>"
	)
	start := strings.Index(text, openTag)
	if start < 0 {
		return ""
	}
	start += len(openTag)
	end := strings.Index(text[start:], closeTag)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(text[start : start+end])
}

func parseResumeChoice(input string, n int) (int, bool) {
	var idx int
	if _, err := fmt.Sscanf(strings.TrimSpace(input), "%d", &idx); err != nil {
		return 0, false
	}
	idx--
	return idx, idx >= 0 && idx < n
}

func shortThread(id string) string {
	if len([]rune(id)) <= 8 {
		return id
	}
	return string([]rune(id)[:8])
}

func renderChatLines(lines []chatLine, width int) []string {
	out := make([]string, 0, len(lines)*2)
	contentWidth := max(20, width-8)
	for _, line := range lines {
		switch line.Kind {
		case lineUser:
			out = append(out, renderRoleBlock("You", line.Text, contentWidth, userStyle)...)
		case lineAssistant:
			out = append(out, renderWrappedBlock(line.Text, contentWidth, "", "  ", textStyle)...)
		case lineReasoning:
			out = append(out, renderWrappedBlock(line.Text, contentWidth, "", "  ", reasoningStyle)...)
		case lineTool:
			out = append(out, renderWrappedBlock(line.Text, width, "• ", "  ", toolStyle)...)
		case lineDetail:
			out = append(out, renderWrappedBlock(line.Text, width, "  └ ", "    ", detailStyle)...)
		case lineError:
			out = append(out, renderWrappedBlock(line.Text, width, "• ", "  ", errorStyle)...)
		default:
			out = append(out, renderWrappedBlock(line.Text, width, "• ", "  ", eventStyle)...)
		}
	}
	return trimTrailingBlankLines(out)
}

func (m chatModel) formatTranscriptLine(line chatLine) string {
	text := strings.TrimRight(line.Text, " \t\r\n")
	if strings.TrimSpace(text) == "" {
		return ""
	}
	switch line.Kind {
	case lineUser:
		return m.renderTranscriptInput(text)
	case lineAssistant:
		return formatAssistantTranscript(text)
	case lineReasoning:
		return transcriptReasoningStyle.Render(text)
	case lineDetail:
		return transcriptDetailStyle.Render(prefixMultiline("  └ ", "    ", text))
	case lineError:
		return transcriptErrorStyle.Render(prefixMultiline("! ", "  ", text))
	case lineTool:
		return transcriptToolStyle.Render(prefixMultiline("◆ ", "  ", text))
	default:
		return transcriptEventStyle.Render(prefixMultiline("• ", "  ", text))
	}
}

func (m chatModel) renderTranscriptInput(text string) string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	return panelStyle.Width(max(30, width-4)).Render("> " + text)
}

func formatAssistantTranscript(text string) string {
	parts := strings.Split(text, "\n")
	if len(parts) == 0 {
		return ""
	}
	parts[0] = transcriptAssistantBulletStyle.Render("• ") + transcriptAssistantStyle.Render(parts[0])
	for i := 1; i < len(parts); i++ {
		parts[i] = "  " + transcriptAssistantStyle.Render(parts[i])
	}
	return strings.Join(parts, "\n")
}

func prefixMultiline(firstPrefix, nextPrefix, text string) string {
	parts := strings.Split(text, "\n")
	for i, part := range parts {
		if i == 0 {
			parts[i] = firstPrefix + part
		} else {
			parts[i] = nextPrefix + part
		}
	}
	return strings.Join(parts, "\n")
}

func visibleBodyLines(body []string, height int, scrollOffset int) []string {
	if height < 1 {
		height = 1
	}
	if len(body) <= height {
		return body
	}
	maxOffset := len(body) - height
	if scrollOffset < 0 {
		scrollOffset = 0
	}
	if scrollOffset > maxOffset {
		scrollOffset = maxOffset
	}
	end := len(body) - scrollOffset
	start := end - height
	if start < 0 {
		start = 0
	}
	return body[start:end]
}

func renderRoleBlock(role, text string, width int, style lipgloss.Style) []string {
	parts := wrapText(text, width)
	out := make([]string, 0, len(parts))
	for i, part := range parts {
		prefix := "  "
		if i == 0 {
			prefix = role + ": "
		}
		out = append(out, style.Render(prefix+part))
	}
	return out
}

func renderWrappedBlock(text string, width int, firstPrefix string, nextPrefix string, style lipgloss.Style) []string {
	contentWidth := width - len([]rune(nextPrefix))
	if firstWidth := width - len([]rune(firstPrefix)); firstWidth < contentWidth {
		contentWidth = firstWidth
	}
	if contentWidth < 10 {
		contentWidth = max(1, width)
	}
	parts := wrapText(text, contentWidth)
	out := make([]string, 0, len(parts))
	for i, part := range parts {
		prefix := nextPrefix
		if i == 0 {
			prefix = firstPrefix
		}
		out = append(out, style.Render(prefix+part))
	}
	return out
}

func wrapText(text string, width int) []string {
	if width <= 4 {
		return []string{text}
	}
	if text == "" {
		return []string{""}
	}
	rawLines := strings.Split(text, "\n")
	out := make([]string, 0, len(rawLines))
	for _, rawLine := range rawLines {
		if rawLine == "" {
			out = append(out, "")
			continue
		}
		runes := []rune(rawLine)
		for len(runes) > width {
			out = append(out, string(runes[:width]))
			runes = runes[width:]
		}
		out = append(out, string(runes))
	}
	return out
}

func renderedLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func trimTrailingBlankLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func tailLines(lines []string, limit int) []string {
	if limit <= 0 || len(lines) <= limit {
		return lines
	}
	return lines[len(lines)-limit:]
}

func truncateForChat(s string, limit int) string {
	return truncateRunes(strings.TrimSpace(s), limit)
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "..."
}

func dropLastRune(s string) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return s
	}
	return string(runes[:len(runes)-1])
}

func compactJSON(raw string) string {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return strings.TrimSpace(raw)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return string(data)
}

func formatToolStartSummary(name, args string) string {
	name = firstNonEmpty(name, "unknown")
	switch name {
	case execmw.DefaultToolName:
		if command := toolCommandFromArgs(args); command != "" {
			return formatExecCommandStartSummary(command)
		}
		return "Run " + name
	case constant.ToolLs:
		return "List " + firstNonEmpty(toolStringArg(args, "path"), ".")
	case constant.ToolReadFile:
		path := firstNonEmpty(toolStringArg(args, "path"), "<missing path>")
		if suffix := readFileRangeSuffix(args); suffix != "" {
			return "Read " + path + " " + suffix
		}
		return "Read " + path
	case constant.ToolGlob:
		pattern := firstNonEmpty(toolStringArg(args, "pattern"), "<pattern>")
		path := firstNonEmpty(toolStringArg(args, "path"), ".")
		return "Glob " + pattern + " in " + path
	case constant.ToolGrep:
		pattern := firstNonEmpty(toolStringArg(args, "pattern"), "<pattern>")
		path := firstNonEmpty(toolStringArg(args, "path"), ".")
		if glob := toolStringArg(args, "glob"); glob != "" {
			return "Search " + pattern + " in " + path + " (" + glob + ")"
		}
		return "Search " + pattern + " in " + path
	case tasktool.ToolWaitMessage:
		target := firstNonEmpty(toolStringArg(args, "target"), "targets")
		messageID := toolStringArg(args, "message_id")
		if messageID != "" {
			return "Wait for " + target + " / " + shortToken(messageID)
		}
		return "Wait for " + target
	case tasktool.ToolSpawnTask:
		title := firstNonEmpty(toolStringArg(args, "title"), "task")
		if role := toolStringArg(args, "role"); role != "" {
			return "Spawn " + role + ": " + title
		}
		return "Spawn task: " + title
	case constant.ToolWriteFile:
		return "Write " + firstNonEmpty(toolStringArg(args, "path"), "<missing path>")
	case constant.ToolEditFile:
		return "Edit " + firstNonEmpty(toolStringArg(args, "path"), "<missing path>")
	case constant.ToolApplyPatch:
		return formatApplyPatchStartSummary(args)
	}
	if compacted := compactJSON(args); strings.TrimSpace(compacted) != "" {
		return fmt.Sprintf("Calling %s with %s", name, truncateForChat(compacted, 240))
	}
	return "Calling " + name
}

func formatToolEndFallbackSummary(name, args string) string {
	name = firstNonEmpty(name, "unknown")
	if name == execmw.DefaultToolName {
		if command := toolCommandFromArgs(args); command != "" {
			return formatExecCommandStartSummary(command)
		}
		return "Run " + name
	}
	return formatToolStartSummary(name, args)
}

func formatUpdatedPlanDetail(args string) string {
	var payload struct {
		Plan []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(args), &payload); err != nil {
		return ""
	}
	lines := make([]string, 0, len(payload.Plan))
	for _, item := range payload.Plan {
		step := strings.TrimSpace(item.Step)
		if step == "" {
			continue
		}
		lines = append(lines, planStatusSymbol(item.Status)+" "+step)
	}
	return strings.Join(lines, "\n")
}

func planStatusSymbol(status string) string {
	switch strings.TrimSpace(status) {
	case "completed":
		return "✔"
	case "in_progress":
		return "→"
	default:
		return "□"
	}
}

func formatToolResultDetail(name, args, raw string) string {
	return formatToolResultDetailWithWorkDir(name, args, raw, "")
}

func formatToolResultDetailWithWorkDir(name, args, raw, workDir string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	switch name {
	case execmw.DefaultToolName:
		var out struct {
			Output   string `json:"output"`
			ExitCode int    `json:"exit_code"`
			Denied   bool   `json:"denied"`
			Reason   string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(raw), &out); err == nil {
			if output := strings.TrimSpace(out.Output); output != "" {
				return truncateForChat(output, 800)
			}
			if out.Reason != "" {
				return truncateForChat(out.Reason, 800)
			}
			if out.Denied {
				return "denied"
			}
			if out.ExitCode != 0 {
				return fmt.Sprintf("exit_code=%d", out.ExitCode)
			}
			return ""
		}
	case constant.ToolLs, constant.ToolGlob:
		var envelope struct {
			Data []struct {
				Path  string `json:"path"`
				IsDir bool   `json:"is_dir"`
			} `json:"data"`
			ErrMsg string `json:"errmsg"`
		}
		if err := json.Unmarshal([]byte(raw), &envelope); err == nil {
			if envelope.ErrMsg != "" {
				return truncateForChat(envelope.ErrMsg, 800)
			}
			return formatFileListSummary(envelope.Data)
		}
	case constant.ToolReadFile:
		var envelope struct {
			Data   string `json:"data"`
			ErrMsg string `json:"errmsg"`
		}
		if err := json.Unmarshal([]byte(raw), &envelope); err == nil {
			if envelope.ErrMsg != "" {
				return truncateForChat(envelope.ErrMsg, 800)
			}
			return formatReadFileSummary(envelope.Data)
		}
	case constant.ToolGrep:
		var envelope struct {
			Data   []grepMatchSummary `json:"data"`
			ErrMsg string             `json:"errmsg"`
		}
		if err := json.Unmarshal([]byte(raw), &envelope); err == nil {
			if envelope.ErrMsg != "" {
				return truncateForChat(envelope.ErrMsg, 800)
			}
			return formatGrepMatchesSummary(envelope.Data)
		}
	case constant.ToolApplyPatch:
		return formatApplyPatchResultWithWorkDir(args, raw, workDir)
	case tasktool.ToolWaitMessage:
		return formatWaitMessageResult(raw)
	case tasktool.ToolSpawnTask:
		var envelope struct {
			Data struct {
				InitialMessageID string `json:"initial_message_id"`
				Target           string `json:"target"`
				Warning          string `json:"warning"`
			} `json:"data"`
			ErrMsg string `json:"errmsg"`
		}
		if err := json.Unmarshal([]byte(raw), &envelope); err == nil {
			if envelope.ErrMsg != "" {
				return "spawn_task error: " + truncateForChat(envelope.ErrMsg, 800)
			}
			parts := make([]string, 0, 3)
			if envelope.Data.Target != "" {
				parts = append(parts, "target "+shortToken(envelope.Data.Target))
			}
			if envelope.Data.InitialMessageID != "" {
				parts = append(parts, "message "+shortToken(envelope.Data.InitialMessageID))
			}
			if envelope.Data.Warning != "" {
				parts = append(parts, envelope.Data.Warning)
			}
			return strings.Join(parts, ", ")
		}
	case "write_file", "edit_file":
		var envelope struct {
			Data struct {
				Path string `json:"path"`
			} `json:"data"`
			ErrMsg string `json:"errmsg"`
		}
		if err := json.Unmarshal([]byte(raw), &envelope); err == nil {
			if envelope.Data.Path != "" {
				return "path: " + truncateForChat(envelope.Data.Path, 240)
			}
			if envelope.ErrMsg != "" {
				return truncateForChat(envelope.ErrMsg, 800)
			}
			return ""
		}
	}
	return ""
}

func formatExecCommandStartSummary(command string) string {
	command = strings.TrimSpace(command)
	switch {
	case isRipgrepFilesCommand(command):
		return "Find files with rg: " + command
	case isRipgrepCommand(command):
		return "Search with rg: " + command
	default:
		return "Run " + command
	}
}

func isRipgrepCommand(command string) bool {
	command = strings.TrimSpace(command)
	return command == "rg" || strings.HasPrefix(command, "rg ") || strings.HasPrefix(command, "rg\t")
}

func isRipgrepFilesCommand(command string) bool {
	if !isRipgrepCommand(command) {
		return false
	}
	for _, field := range strings.Fields(command) {
		if field == "--files" {
			return true
		}
	}
	return false
}

type grepMatchSummary struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

func formatGrepMatchesSummary(matches []grepMatchSummary) string {
	if len(matches) == 0 {
		return "No matches"
	}
	parts := make([]string, 0, minInt(len(matches), 3))
	for _, match := range matches {
		if len(parts) == 3 {
			break
		}
		loc := match.Path
		if match.Line > 0 {
			loc = fmt.Sprintf("%s:%d", loc, match.Line)
		}
		text := strings.TrimSpace(match.Text)
		if text != "" {
			parts = append(parts, loc+": "+truncateForChat(text, 120))
		} else {
			parts = append(parts, loc)
		}
	}
	return fmt.Sprintf("%d %s: %s", len(matches), plural(len(matches), "match", "matches"), strings.Join(parts, "; "))
}

type patchChangeSummary struct {
	Action    string
	Path      string
	Additions int
	Deletions int
	Hunks     [][]string
}

func formatApplyPatchStartSummary(args string) string {
	changes := patchChangesFromArgs(args)
	if len(changes) == 0 {
		return "Apply patch"
	}
	return formatPatchHeader(changes)
}

func formatApplyPatchResult(args, raw string) string {
	return formatApplyPatchResultWithWorkDir(args, raw, "")
}

func formatApplyPatchResultWithWorkDir(args, raw, workDir string) string {
	var envelope struct {
		Data   string `json:"data"`
		ErrMsg string `json:"errmsg"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err == nil {
		if envelope.ErrMsg != "" {
			return truncateForChat(envelope.ErrMsg, 800)
		}
		if changes := patchChangesFromArgs(args); len(changes) > 0 {
			if preview := formatPatchPreview(changes, workDir); preview != "" {
				return preview
			}
		}
		if output := cleanApplyPatchOutput(envelope.Data); output != "" && !isApplyPatchSuccessOutput(output) {
			return truncateForChat(output, 800)
		}
	}
	if changes := patchChangesFromArgs(args); len(changes) > 0 {
		return formatPatchPreview(changes, workDir)
	}
	return ""
}

func patchChangesFromArgs(args string) []patchChangeSummary {
	var payload struct {
		Patch string `json:"patch"`
	}
	if err := json.Unmarshal([]byte(args), &payload); err != nil {
		return nil
	}
	return patchChangesFromPatch(payload.Patch)
}

func patchChangesFromPatch(patch string) []patchChangeSummary {
	var changes []patchChangeSummary
	current := -1
	inHunk := false
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			changes = append(changes, patchChangeSummary{Action: "add", Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))})
			current = len(changes) - 1
			inHunk = true
			startNewPatchHunk(&changes[current])
		case strings.HasPrefix(line, "*** Update File: "):
			changes = append(changes, patchChangeSummary{Action: "edit", Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))})
			current = len(changes) - 1
			inHunk = false
		case strings.HasPrefix(line, "*** Delete File: "):
			changes = append(changes, patchChangeSummary{Action: "delete", Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))})
			current = len(changes) - 1
			inHunk = false
		case strings.HasPrefix(line, "*** "):
			inHunk = false
		case strings.HasPrefix(line, "@@") && current >= 0:
			inHunk = true
			startNewPatchHunk(&changes[current])
		case inHunk && current >= 0:
			recordPatchLine(&changes[current], line)
		}
	}
	return changes
}

func startNewPatchHunk(change *patchChangeSummary) {
	if change == nil {
		return
	}
	if len(change.Hunks) == 0 || len(change.Hunks[len(change.Hunks)-1]) > 0 {
		change.Hunks = append(change.Hunks, nil)
	}
}

func ensurePatchHunk(change *patchChangeSummary) {
	if change != nil && len(change.Hunks) == 0 {
		change.Hunks = append(change.Hunks, nil)
	}
}

func recordPatchLine(change *patchChangeSummary, line string) {
	if change == nil || line == "" {
		return
	}
	switch line[0] {
	case '+':
		change.Additions++
		appendPatchHunkLine(change, line)
	case '-':
		change.Deletions++
		appendPatchHunkLine(change, line)
	case ' ':
		appendPatchHunkLine(change, line)
	}
}

func appendPatchHunkLine(change *patchChangeSummary, line string) {
	if change == nil {
		return
	}
	ensurePatchHunk(change)
	last := len(change.Hunks) - 1
	if strings.TrimSpace(line) == "" && len(change.Hunks[last]) == 0 {
		return
	}
	change.Hunks[last] = append(change.Hunks[last], line)
}

func formatPatchHeader(changes []patchChangeSummary) string {
	if len(changes) == 1 {
		change := changes[0]
		return fmt.Sprintf("%s %s %s", patchActionPastTense(change.Action), change.Path, formatPatchStats(change))
	}
	stats := totalPatchStats(changes)
	return fmt.Sprintf("Edited %d files %s", len(changes), stats)
}

func patchActionPastTense(action string) string {
	switch action {
	case "add":
		return "Added"
	case "delete":
		return "Deleted"
	default:
		return "Edited"
	}
}

func totalPatchStats(changes []patchChangeSummary) string {
	var stats patchChangeSummary
	for _, change := range changes {
		stats.Additions += change.Additions
		stats.Deletions += change.Deletions
	}
	return formatPatchStats(stats)
}

func formatPatchStats(change patchChangeSummary) string {
	parts := make([]string, 0, 2)
	if change.Additions > 0 {
		parts = append(parts, fmt.Sprintf("+%d", change.Additions))
	}
	if change.Deletions > 0 {
		parts = append(parts, fmt.Sprintf("-%d", change.Deletions))
	}
	if len(parts) == 0 {
		return "(no line changes)"
	}
	return "(" + strings.Join(parts, " ") + ")"
}

func formatPatchPreview(changes []patchChangeSummary, workDir string) string {
	if len(changes) == 0 {
		return ""
	}
	var lines []string
	for i, change := range changes {
		if i > 0 {
			lines = append(lines, "")
		}
		if len(changes) > 1 {
			lines = append(lines, fmt.Sprintf("%s %s %s", patchActionPastTense(change.Action), change.Path, formatPatchStats(change)))
		}
		fileLines := readPatchPreviewFile(workDir, change.Path)
		for hunkIndex, hunk := range change.Hunks {
			if len(hunk) == 0 {
				continue
			}
			if hunkIndex > 0 {
				lines = append(lines, patchEllipsisLine(fileLines))
			}
			lines = append(lines, formatPatchHunkPreview(hunk, fileLines)...)
		}
		if len(lines) >= 40 {
			lines = append(lines[:40], "...")
			break
		}
	}
	return strings.Join(lines, "\n")
}

func readPatchPreviewFile(workDir, path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	candidates := []string{path}
	if workDir != "" && !filepath.IsAbs(path) {
		candidates = append([]string{filepath.Join(workDir, path)}, candidates...)
	}
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err == nil {
			return strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		}
	}
	return nil
}

func patchEllipsisLine(fileLines []string) string {
	return fmt.Sprintf("%s ⋮", patchLineNumberGutter(maxPatchLineNumber(fileLines), 0))
}

func formatPatchHunkPreview(hunk []string, fileLines []string) []string {
	start := locatePatchHunkStart(fileLines, hunk)
	maxLine := maxPatchLineNumber(fileLines)
	var out []string
	newLine := start
	oldLine := start
	if start == 0 {
		newLine = 0
		oldLine = 0
	}
	for _, raw := range hunk {
		if raw == "" {
			continue
		}
		kind := raw[0]
		if kind != '+' && kind != '-' && kind != ' ' {
			continue
		}
		text := raw[1:]
		switch kind {
		case '+':
			out = append(out, formatPatchDiffLine(maxLine, newLine, '+', text))
			if newLine > 0 {
				newLine++
			}
		case '-':
			out = append(out, formatPatchDiffLine(maxLine, oldLine, '-', text))
			if oldLine > 0 {
				oldLine++
			}
		default:
			out = append(out, formatPatchDiffLine(maxLine, newLine, ' ', text))
			if newLine > 0 {
				newLine++
			}
			if oldLine > 0 {
				oldLine++
			}
		}
	}
	return out
}

func locatePatchHunkStart(fileLines []string, hunk []string) int {
	if len(fileLines) == 0 {
		return 0
	}
	var finalLines []string
	for _, raw := range hunk {
		if raw == "" || raw[0] == '-' {
			continue
		}
		if raw[0] == '+' || raw[0] == ' ' {
			finalLines = append(finalLines, raw[1:])
		}
	}
	if len(finalLines) == 0 {
		return 0
	}
	for i := 0; i+len(finalLines) <= len(fileLines); i++ {
		match := true
		for j := range finalLines {
			if fileLines[i+j] != finalLines[j] {
				match = false
				break
			}
		}
		if match {
			return i + 1
		}
	}
	return 0
}

func formatPatchDiffLine(maxLine, lineNumber int, sign byte, text string) string {
	return fmt.Sprintf("%s %c%s", patchLineNumberGutter(maxLine, lineNumber), sign, text)
}

func patchLineNumberGutter(maxLine, lineNumber int) string {
	width := len(fmt.Sprint(max(1, maxLine)))
	if lineNumber <= 0 {
		return strings.Repeat(" ", width)
	}
	return fmt.Sprintf("%*d", width, lineNumber)
}

func maxPatchLineNumber(fileLines []string) int {
	if len(fileLines) == 0 {
		return 1
	}
	return len(fileLines)
}

func cleanApplyPatchOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	replacements := []struct {
		old string
		new string
	}{
		{"stdout>\n", ""},
		{"\nstdout end", ""},
		{"stderr>\n", "stderr: "},
		{"\nstderr end", ""},
	}
	for _, repl := range replacements {
		output = strings.ReplaceAll(output, repl.old, repl.new)
	}
	return strings.TrimSpace(output)
}

func isApplyPatchSuccessOutput(output string) bool {
	output = strings.TrimSpace(output)
	return strings.HasPrefix(output, "Success. Updated the following files:")
}

func toolStringArg(args string, key string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(args), &payload); err != nil {
		return ""
	}
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func readFileRangeSuffix(args string) string {
	var payload struct {
		Offset *int `json:"offset"`
		Limit  *int `json:"limit"`
	}
	if err := json.Unmarshal([]byte(args), &payload); err != nil {
		return ""
	}
	parts := make([]string, 0, 2)
	if payload.Offset != nil {
		parts = append(parts, fmt.Sprintf("offset=%d", *payload.Offset))
	}
	if payload.Limit != nil {
		parts = append(parts, fmt.Sprintf("limit=%d", *payload.Limit))
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, " ") + ")"
}

func formatFileListSummary(files []struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}) string {
	count := len(files)
	if count == 0 {
		return "0 entries"
	}
	names := make([]string, 0, minInt(count, 5))
	for i, file := range files {
		if i >= 5 {
			break
		}
		name := file.Path
		if file.IsDir && !strings.HasSuffix(name, "/") {
			name += "/"
		}
		names = append(names, name)
	}
	suffix := ""
	if count > len(names) {
		suffix = fmt.Sprintf(", +%d more", count-len(names))
	}
	return fmt.Sprintf("%d %s: %s%s", count, plural(count, "entry", "entries"), strings.Join(names, ", "), suffix)
}

func formatReadFileSummary(data string) string {
	data = strings.TrimSpace(data)
	if data == "" {
		return "empty file"
	}
	lineCount := strings.Count(data, "\n") + 1
	preview := firstContentPreview(data)
	if preview == "" {
		return fmt.Sprintf("%d %s", lineCount, plural(lineCount, "line", "lines"))
	}
	return fmt.Sprintf("%d %s, preview: %s", lineCount, plural(lineCount, "line", "lines"), truncateForChat(preview, 160))
}

func firstContentPreview(data string) string {
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.Index(line, "|"); idx >= 0 {
			line = strings.TrimSpace(line[idx+1:])
		}
		if line != "" {
			return line
		}
	}
	return ""
}

func shortToken(value string) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) <= 8 {
		return value
	}
	return string([]rune(value)[:8])
}

func plural(n int, one string, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func toolCommandFromArgs(args string) string {
	var payload struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(args), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Cmd)
}

func formatWaitMessageResult(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var envelope struct {
		Data struct {
			Res map[string]struct {
				Result   string `json:"result"`
				Done     bool   `json:"done"`
				TimedOut bool   `json:"timed_out"`
				State    string `json:"state"`
				SysError string `json:"sys_error"`
			} `json:"res"`
		} `json:"data"`
		Errmsg string `json:"errmsg"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return truncateForChat(raw, 500)
	}
	if envelope.Errmsg != "" {
		return "wait_message error: " + envelope.Errmsg
	}
	results := make([]string, 0, len(envelope.Data.Res))
	for target, item := range envelope.Data.Res {
		label := shortWaitTarget(target)
		switch {
		case item.SysError != "":
			results = append(results, label+" error: "+item.SysError)
		case item.TimedOut:
			state := firstNonEmpty(item.State, "waiting")
			results = append(results, label+" timed out, state="+state)
		case item.Done && item.Result != "":
			results = append(results, label+" completed: "+truncateForChat(strings.TrimSpace(item.Result), 500))
		case item.Done:
			results = append(results, label+" completed")
		case item.Result != "":
			state := firstNonEmpty(item.State, "waiting")
			results = append(results, label+" state="+state+": "+truncateForChat(strings.TrimSpace(item.Result), 500))
		case item.State != "":
			results = append(results, label+" state="+item.State)
		}
	}
	if len(results) == 0 {
		return ""
	}
	sort.Strings(results)
	return truncateForChat(strings.Join(results, "\n"), 500)
}

func shortWaitTarget(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return "target"
	}
	parts := strings.Split(target, "/")
	for i := range parts {
		parts[i] = shortToken(parts[i])
	}
	return strings.Join(parts, "/")
}

func threadStatus(thread *threadViewState) string {
	if thread == nil {
		return ""
	}
	switch thread.Activity {
	case "tool":
		return "running · tool " + firstNonEmpty(thread.ToolName, "unknown")
	case "thinking":
		return "running · thinking"
	case "blocked":
		if thread.ToolName != "" {
			return "blocked · approval " + thread.ToolName
		}
		return "blocked"
	case "idle":
		return "idle"
	case "error":
		return "error"
	case "running":
		return "running"
	default:
		return ""
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
