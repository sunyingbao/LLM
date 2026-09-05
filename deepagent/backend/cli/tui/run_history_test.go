package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	protoinput "eino-cli/deepagent/cloud/protocol/input"
	sdkruntime "eino-cli/deepagent/runtime"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"eino-cli/deepagent/backend/config"
	"eino-cli/deepagent/backend/consts"
	"eino-cli/deepagent/backend/session/rollback"
	"eino-cli/deepagent/backend/session/runs"
	runtimeRun "eino-cli/deepagent/host/run"
	clientruntime "eino-cli/deepagent/host/runtime"
)

type historyRuntime struct{}

func (r *historyRuntime) SetPlanMode(_ context.Context, on bool) (bool, error) { return on, nil }

func (r *historyRuntime) Name() string { return "history-runtime" }

func (r *historyRuntime) StartTurn(context.Context, string) (*clientruntime.TurnStream, error) {
	return nil, nil
}
func (r *historyRuntime) Resume(context.Context, sdkruntime.GlobalThreadRef, protoinput.ResumeTurnPayload) error {
	return nil
}
func (r *historyRuntime) ClearThread()                     {}
func (r *historyRuntime) ExportThreadRef() ([]byte, error) { return []byte("[]"), nil }
func (r *historyRuntime) ImportThreadRef(payload []byte) error {
	return nil
}
func (r *historyRuntime) RuntimeKind() sdkruntime.RuntimeKind { return sdkruntime.RuntimeLocal }

func TestRunHistoryRenderAndKeys(t *testing.T) {
	m := &Model{
		width:          80,
		runHistoryOpen: true,
		runHistoryRows: []runs.Record{
			{ID: "run-newest", Status: "success", Prompt: "newest prompt", Rollbackable: true},
			{ID: "run-older", Status: "success", Prompt: "older prompt"},
		},
	}
	panel := renderRunHistoryPanel(m)
	if !strings.Contains(panel, "Run history") || !strings.Contains(panel, "newest prompt") {
		t.Fatalf("unexpected history panel:\n%s", panel)
	}
	if _, handled := applyRunHistoryKey(m, tea.KeyMsg{Type: tea.KeyDown}); !handled {
		t.Fatal("down key should be handled")
	}
	if m.runHistorySel != 1 {
		t.Fatalf("selection = %d, want 1", m.runHistorySel)
	}
	if _, handled := applyRunHistoryKey(m, tea.KeyMsg{Type: tea.KeyEsc}); !handled {
		t.Fatal("esc key should be handled")
	}
	if m.runHistoryOpen {
		t.Fatal("history panel should close")
	}
}

func TestRunHistoryRestoresWorkspaceWithoutChangingThreadHistory(t *testing.T) {
	root := t.TempDir()
	cleanup := config.SetRootDirForTest(root)
	defer cleanup()
	runStore := runs.NewStore(config.SessionRunsDir(consts.DefaultSessionID))
	rollbackStore := rollback.NewStore(root, consts.DefaultSessionID)
	workspaceFile := filepath.Join(config.SandboxWorkDir(consts.DefaultSessionID), "state.txt")
	if err := os.MkdirAll(filepath.Dir(workspaceFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspaceFile, []byte("run-one"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	run1 := runs.Record{
		ID:        "run-1",
		Status:    "success",
		Prompt:    "prompt one",
		Output:    "answer one",
		CreatedAt: now,
		UpdatedAt: now,
	}
	run2 := runs.Record{
		ID:        "run-2",
		Status:    "success",
		Prompt:    "prompt two",
		Output:    "answer two",
		CreatedAt: now.Add(time.Second),
		UpdatedAt: now.Add(time.Second),
	}
	saveRollbackableRecord(t, runStore, rollbackStore, &run1)
	if err := os.WriteFile(workspaceFile, []byte("run-two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runStore.Save(context.Background(), run2); err != nil {
		t.Fatal(err)
	}
	saveRollbackableRecord(t, runStore, rollbackStore, &run2)

	rt := &historyRuntime{}
	m := &Model{
		rt:             rt,
		runs:           runtimeRun.NewManagerWithStore(runStore, rollbackStore),
		width:          80,
		height:         30,
		viewport:       viewport.New(80, 10),
		modelName:      "history-runtime",
		cwd:            root,
		messages:       freshMessages(80, "history-runtime", root),
		runHistoryOpen: true,
		runHistoryRows: []runs.Record{run2, run1},
		runHistorySel:  1,
	}

	rollbackSelectedRun(m)

	workspace, err := os.ReadFile(workspaceFile)
	if err != nil || string(workspace) != "run-one" {
		t.Fatalf("workspace=%q error=%v", workspace, err)
	}
	body := historyMessageBody(m.messages)
	if !strings.Contains(body, "workspace restored from run-1") || !strings.Contains(body, "next message starts a new turn") {
		t.Fatalf("workspace restore confirmation missing:\n%s", body)
	}
}

func saveRollbackableRecord(t *testing.T, runStore *runs.Store, rollbackStore *rollback.Store, rec *runs.Record) {
	t.Helper()
	if err := runStore.Save(context.Background(), *rec); err != nil {
		t.Fatal(err)
	}
	path, err := rollbackStore.SaveWorkspacePost(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec.Rollbackable = true
	rec.RollbackPath = path
	if err := runStore.Save(context.Background(), *rec); err != nil {
		t.Fatal(err)
	}
}

func historyMessageBody(messages []chatMessage) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString(msg.Content)
		b.WriteString("\n")
	}
	return b.String()
}
