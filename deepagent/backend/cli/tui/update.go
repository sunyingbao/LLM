package tui

import (
	"fmt"
	"strings"
	"time"

	sdkruntime "eino-cli/deepagent/runtime"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) Update(msg tea.Msg) (model tea.Model, cmd tea.Cmd) {
	defer func() {
		if model == nil {
			model = m
		}
		if next, ok := model.(*Model); ok {
			cmd = tea.Batch(cmd, flushScrollbackCmd(next))
		}
	}()
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return applyResize(m, msg)
	case tea.KeyMsg:
		return applyKey(m, msg)
	case chunkMsg:
		drainApprovals(m)
		drainQuestions(m)
		m.streamBuf.WriteString(string(msg))
		return m, waitForStreamMsg(m.streamCh)
	case streamOutputMsg:
		drainApprovals(m)
		drainQuestions(m)
		m.streamBuf.Reset()
		m.streamBuf.WriteString(string(msg))
		return m, waitForStreamMsg(m.streamCh)
	case doneMsg:
		return applyDone(m, msg)
	case streamStartedMsg:
		m.streamCh = msg.messages
		m.cancel = msg.cancel
		m.detach = msg.detach
		return m, waitForStreamMsg(m.streamCh)
	case stopResultMsg:
		if msg.err != nil {
			m.interrupted = false
			m.lastErr = msg.err
			pushMessage(m, "system", fmt.Sprintf("stop failed: %v; retry Ctrl-C or press Ctrl-D to detach", msg.err))
		} else {
			m.footerHint = "stop requested; waiting for backend terminal event"
		}
		return m, waitForStreamMsg(m.streamCh)
	case remoteSessionsMsg:
		m.streaming = false
		if msg.err != nil {
			pushMessage(m, "system", fmt.Sprintf("sessions: %v", msg.err))
			return m, nil
		}
		if len(msg.sessions) == 0 {
			pushMessage(m, "system", "sessions: no backend sessions")
			return m, nil
		}
		lines := []string{"Backend sessions:"}
		for _, session := range msg.sessions {
			title := strings.TrimSpace(session.Title)
			if title == "" {
				title = "untitled"
			}
			lines = append(lines, fmt.Sprintf("- %s  %s", session.ID, title))
		}
		pushMessage(m, "system", strings.Join(lines, "\n"))
		return m, nil
	case remoteSessionLoadedMsg:
		if msg.err != nil {
			drainApprovals(m)
			drainQuestions(m)
			m.streaming = false
			m.lastErr = msg.err
			pushMessage(m, "system", fmt.Sprintf("resume: %v", msg.err))
			return m, nil
		}
		detachStream(m)
		m.modelName, m.cwd = remoteSessionIdentity(msg.session)
		applyRemoteHistory(m, msg.history)
		if msg.stream == nil {
			m.streaming = false
			pushMessage(m, "system", fmt.Sprintf("session %s history loaded", msg.session.ID))
			return m, nil
		}
		started := followStream(m.rt, msg.ctx, msg.cancel, msg.stream)
		m.streaming = true
		m.streamStart = time.Now()
		m.streamBuf.Reset()
		m.streamCh = started.messages
		m.cancel = started.cancel
		m.detach = started.detach
		return m, tea.Batch(waitForStreamMsg(m.streamCh), m.spin.Tick)
	case timelineMsg:
		model, cmd = applyTimelineEvent(m, msg.event)
		if m.streaming {
			cmd = tea.Batch(cmd, waitForStreamMsg(m.streamCh))
		}
		return model, cmd
	case approvalRequest:
		model, cmd = applyApprovalRequest(m, msg)
		if m.streaming {
			cmd = tea.Batch(cmd, waitForStreamMsg(m.streamCh))
		}
		return model, cmd
	case questionRequest:
		model, cmd = applyQuestionRequest(m, msg)
		if m.streaming {
			cmd = tea.Batch(cmd, waitForStreamMsg(m.streamCh))
		}
		return model, cmd
	case footerHintExpiredMsg:
		m.footerHint = ""
		return m, nil
	case spinner.TickMsg:
		if !m.streaming {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		m.elapsed = time.Since(m.streamStart).Round(time.Second)
		m.shimmerOffset++
		return m, cmd
	}

	var cmds []tea.Cmd
	var nextCmd tea.Cmd
	m.input, nextCmd = m.input.Update(msg)
	cmds = append(cmds, nextCmd)
	m.viewport, nextCmd = m.viewport.Update(msg)
	cmds = append(cmds, nextCmd)
	return m, tea.Batch(cmds...)
}

func applyResize(m *Model, msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	recomputeLayout(m)

	m.mdRenderer = nil
	for i := range m.messages {
		switch m.messages[i].Role {
		case "banner":
			m.messages[i].Content = renderBanner(m.width, m.modelName, m.cwd)
		case "assistant":
			m.messages[i].Rendered = renderMarkdown(m, m.messages[i].Content)
		}
	}
	rebuildHistory(m)
	m.ready = true
	return m, nil
}

func recomputeLayout(m *Model) {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	streamH := 0
	if len(m.hitlQueue) == 0 && len(m.questionQueue) == 0 && (m.streaming || m.lastErr != nil) {
		streamH = 1
	}
	todoH := getTodoPanelHeight(m)
	popupH := getPopupHeight(m)
	approvalH := 0
	runHistoryH := getRunHistoryPanelHeight(m)
	inputH := 3
	footerH := 1
	if len(m.hitlQueue) > 0 {
		approvalH = approvalPromptHeight + 1
		popupH = 0
		runHistoryH = 0
		inputH = 0
		footerH = 0
	} else if len(m.questionQueue) > 0 {
		front := m.questionQueue[0]
		approvalH = 4
		if front.current >= 0 && front.current < len(front.questions) {
			approvalH += len(front.questions[front.current].options)
		}
		popupH = 0
		runHistoryH = 0
	}
	chrome := streamH + todoH + popupH + approvalH + runHistoryH + inputH + footerH

	vpMax := m.height - chrome
	if vpMax < 3 {
		vpMax = 3
	}
	m.viewport.Width = m.width

	want := m.viewport.TotalLineCount()
	if want < 1 {
		want = 1
	}
	if want > vpMax {
		want = vpMax
	}
	m.viewport.Height = want
	m.input.Width = m.width - 4
}

func getTodoPanelHeight(m *Model) int {
	if len(m.todos) == 0 {
		return 0
	}
	if !m.todoExpanded {
		return 1
	}
	return 2 + len(m.todos)
}

func applyKey(m *Model, msg tea.KeyMsg) (model tea.Model, cmd tea.Cmd) {
	if msg.Type == tea.KeyCtrlD {
		detachStream(m)
		return m, tea.Quit
	}
	if len(m.hitlQueue) > 0 {
		if m.rt != nil && m.rt.RuntimeKind() == sdkruntime.RuntimeRemote && (msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyEsc) {
			drainApprovals(m)
			abortStream(m)
			return m, nil
		}
		if cmd, handled := applyApprovalKey(m, msg); handled {
			return m, cmd
		}
		if msg.Type == tea.KeyCtrlC {
			abortStream(m)
		}
		return m, nil
	}
	if len(m.questionQueue) > 0 {
		return applyQuestionKey(m, msg)
	}
	if m.runHistoryOpen {
		if cmd, handled := applyRunHistoryKey(m, msg); handled {
			return m, cmd
		}
		return m, nil
	}

	if getPopupHeight(m) > 0 {
		if cmd, handled := applyPopupKey(m, msg); handled {
			return m, cmd
		}
	}
	switch msg.String() {
	case "alt+b", "esc+b":
		moveInputWord(m, -1)
		return m, nil
	case "alt+f", "esc+f":
		moveInputWord(m, 1)
		return m, nil
	}
	switch msg.Type {
	case tea.KeyCtrlC:
		if abortStream(m) {
			return m, nil
		}
		if m.pendingExit {
			return m, tea.Quit
		}
		m.pendingExit = true
		pushMessage(m, "system", "Press Ctrl-C again to quit, or type /exit.")
		return m, nil
	case tea.KeyEsc:
		if abortStream(m) {
			return m, nil
		}
		m.input.SetValue("")
		return m, nil
	case tea.KeyEnter:
		if m.streaming {
			return m, nil
		}
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		m.input.Reset()
		m.pendingExit = false
		return submit(m, text)
	}
	if msg.String() == "ctrl+o" {
		if block := getLatestCollapsibleToolBlock(m); block != nil {
			if block.flushed {
				m.pendingScrollback = append(m.pendingScrollback, renderExpandedToolBlockCopy(block))
				m.footerHint = "printed expanded tool block"
				return m, expireFooterHint(3 * time.Second)
			}
			block.collapsed = !block.collapsed
			rebuildHistory(m)
			return m, nil
		}
		m.footerHint = "nothing to expand"
		return m, expireFooterHint(3 * time.Second)
	}

	var cmds []tea.Cmd
	prevValue := m.input.Value()
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	if m.input.Value() != prevValue {
		applyInputChanged(m)
	}
	return m, tea.Batch(cmds...)
}

func applyApprovalRequest(m *Model, req approvalRequest) (tea.Model, tea.Cmd) {
	m.hitlQueue = append(m.hitlQueue, req)
	recomputeLayout(m)
	return m, nil
}

func applyQuestionRequest(m *Model, req questionRequest) (model tea.Model, cmd tea.Cmd) {
	if req.answers == nil {
		req.answers = make(map[string][]string)
	}
	m.questionQueue = append(m.questionQueue, req)
	m.input.Reset()
	m.input.Placeholder = "Type your answer..."
	recomputeLayout(m)
	return m, nil
}

func applyQuestionKey(m *Model, msg tea.KeyMsg) (model tea.Model, cmd tea.Cmd) {
	if msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyEsc {
		abortStream(m)
		drainQuestions(m)
		return m, nil
	}
	if msg.Type != tea.KeyEnter {
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	answer := strings.TrimSpace(m.input.Value())
	if answer == "" {
		return m, nil
	}
	front := &m.questionQueue[0]
	question := front.questions[front.current]
	front.answers[question.id] = []string{answer}
	front.current++
	m.input.Reset()
	if front.current < len(front.questions) {
		recomputeLayout(m)
		return m, nil
	}
	answers := front.answers
	reply := front.reply
	m.questionQueue = m.questionQueue[1:]
	m.input.Placeholder = "Ask anything... (/help for commands)"
	select {
	case reply <- answers:
	default:
	}
	recomputeLayout(m)
	return m, nil
}

func applyApprovalKey(m *Model, msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.Type {
	case tea.KeyEnter, tea.KeyEsc:
		resolveApproval(m, false)
		return nil, true
	case tea.KeyCtrlC:
		resolveApproval(m, false)
		return nil, false
	case tea.KeyRunes:
		switch strings.ToLower(string(msg.Runes)) {
		case "y":
			resolveApproval(m, true)
			return nil, true
		case "n":
			resolveApproval(m, false)
			return nil, true
		}
	}
	return nil, false
}

func resolveApproval(m *Model, approved bool) {
	if len(m.hitlQueue) == 0 {
		return
	}
	front := m.hitlQueue[0]
	select {
	case front.reply <- approved:
	default:
	}
	m.hitlQueue = m.hitlQueue[1:]
	recomputeLayout(m)
}

func drainApprovals(m *Model) {
	if len(m.hitlQueue) == 0 {
		return
	}
	m.hitlQueue = nil
	recomputeLayout(m)
}

func drainQuestions(m *Model) {
	if len(m.questionQueue) == 0 {
		return
	}
	m.questionQueue = nil
	m.input.Placeholder = "Ask anything... (/help for commands)"
	recomputeLayout(m)
}

func applyInputChanged(m *Model) {
	matches := getPopupMatches(m)
	if m.popupSel < 0 || m.popupSel >= len(matches) {
		m.popupSel = 0
	}
	recomputeLayout(m)
}

func applyPopupKey(m *Model, msg tea.KeyMsg) (tea.Cmd, bool) {
	matches := getPopupMatches(m)
	if len(matches) == 0 {
		return nil, false
	}
	switch msg.Type {
	case tea.KeyUp, tea.KeyCtrlP:
		m.popupSel = (m.popupSel - 1 + len(matches)) % len(matches)
		return nil, true
	case tea.KeyDown, tea.KeyCtrlN:
		m.popupSel = (m.popupSel + 1) % len(matches)
		return nil, true
	case tea.KeyTab:
		applyPopupSelection(m, matches[m.popupSel])
		applyInputChanged(m)
		return nil, true
	case tea.KeyEnter:
		applyPopupSelection(m, matches[m.popupSel])
		applyInputChanged(m)
		return nil, false
	case tea.KeyEsc:
		m.input.SetValue("")
		applyInputChanged(m)
		return nil, true
	}
	return nil, false
}

func applyPopupSelection(m *Model, c slashCommand) {
	m.input.SetValue("/" + c.Name)
	if c.Type == "builtin" || c.Type == "skill" {
		m.input.SetValue("/" + c.Name + " ")
	}
	m.input.CursorEnd()
}

func submit(m *Model, text string) (model tea.Model, cmd tea.Cmd) {
	if cmd, handled := applyBuiltin(m, text); handled {
		return m, cmd
	}

	pushMessage(m, "user", text)
	m.streaming = true
	m.streamBuf.Reset()
	m.lastErr = nil
	m.interrupted = false

	m.verbPresent, m.verbPast = pickVerb()
	m.streamStart = time.Now()
	m.elapsed = 0

	return m, tea.Batch(startStreamCmd(m.rt, m.sessionID, text, m.runs), m.spin.Tick)
}

func pushToolBlockMessage(m *Model, content string) {
	if n := len(m.messages); n > 0 && m.messages[n-1].Role == "assistant" {
		m.messages = append(m.messages, chatMessage{})
		copy(m.messages[n:], m.messages[n-1:])
		m.messages[n-1] = chatMessage{Role: "tool-block", Content: content}
		rebuildHistory(m)
		return
	}
	pushMessage(m, "tool-block", content)
}

func applyDone(m *Model, msg doneMsg) (model tea.Model, cmd tea.Cmd) {
	elapsed := time.Since(m.streamStart).Round(time.Second)
	drainedCmd := drainQueuedStreamMessages(m)
	interrupted := m.interrupted

	m.cancel = nil
	m.detach = nil
	m.streamCh = nil
	m.interrupted = false
	drainApprovals(m)
	drainQuestions(m)

	if msg.err != nil {
		if buf := strings.TrimSpace(m.streamBuf.String()); buf != "" {
			pushMessage(m, "assistant", buf)
		}
		if interrupted {
			m.lastErr = nil
			pushMessage(m, "system", "Conversation interrupted")
		} else {
			m.lastErr = msg.err
			pushMessage(m, "system", fmt.Sprintf("error: %s", msg.err))
		}
		m.streamBuf.Reset()
		m.streaming = false
		queueCompletedTurnScrollback(m)
		return m, drainedCmd
	}

	final := strings.TrimSpace(msg.output)
	if final == "" {
		final = strings.TrimSpace(m.streamBuf.String())
	}
	if final != "" {
		pushMessage(m, "assistant", final)
	}
	if interrupted {
		pushMessage(m, "system", "Conversation interrupted")
	}

	const summaryThreshold = 2 * time.Second
	if !interrupted && elapsed >= summaryThreshold && m.verbPast != "" {
		pushMessage(m, "thinking-summary",
			fmt.Sprintf("%s for %ds", m.verbPast, int(elapsed.Seconds())))
	}

	m.streamBuf.Reset()
	m.streaming = false
	queueCompletedTurnScrollback(m)
	return m, drainedCmd
}

func drainQueuedStreamMessages(m *Model) (cmd tea.Cmd) {
	var cmds []tea.Cmd
	for m.streamCh != nil {
		select {
		case queued, ok := <-m.streamCh:
			if !ok {
				return tea.Batch(cmds...)
			}
			switch v := queued.(type) {
			case chunkMsg:
				m.streamBuf.WriteString(string(v))
			case streamOutputMsg:
				m.streamBuf.Reset()
				m.streamBuf.WriteString(string(v))
			case timelineMsg:
				_, cmd := applyTimelineEvent(m, v.event)
				cmds = append(cmds, cmd)
			}
		default:
			return tea.Batch(cmds...)
		}
	}
	return tea.Batch(cmds...)
}
