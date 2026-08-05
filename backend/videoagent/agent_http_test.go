package videoagent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	modelcomponent "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestAgentChatOperationCanStartRunAfterConfirmation(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()

	handler := NewHTTPHandler(application)
	request := httptest.NewRequest(http.MethodPost, "/agent/chat", bytes.NewBufferString(`{"project_id":"demo","text":"请生成一条广告","product_name":"shoe","brief":"15 second video"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("POST /agent/chat status = %d, want %d", response.Code, http.StatusOK)
	}
	var agentResponse AgentChatReply
	if err := json.NewDecoder(response.Body).Decode(&agentResponse); err != nil {
		t.Fatalf("decode agent response: %v", err)
	}
	if agentResponse.Operation == nil {
		t.Fatal("agent response did not contain an operation")
	}
	if len(agentResponse.Messages) != 1 || agentResponse.Operation.SourceMessageID != agentResponse.Messages[0].ID {
		t.Fatalf("operation source message = %q, reply messages = %#v", agentResponse.Operation.SourceMessageID, agentResponse.Messages)
	}

	confirmRequest := httptest.NewRequest(http.MethodPost, "/operations/"+agentResponse.Operation.ID+"/confirm", nil)
	confirm := httptest.NewRecorder()
	handler.ServeHTTP(confirm, confirmRequest)
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm operation status = %d, want %d", confirm.Code, http.StatusOK)
	}
	var result struct {
		Run *Run `json:"run"`
	}
	if err := json.NewDecoder(confirm.Body).Decode(&result); err != nil {
		t.Fatalf("decode confirm response: %v", err)
	}
	if result.Run == nil {
		t.Fatal("confirm response did not contain a run")
	}
}

func TestAgentChatCreatesProjectBeforeRunOperation(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()

	reply, err := application.Agent.Chat(context.Background(), AgentChatInput{
		ProjectID: "new-project", Text: "开始生成", RunInput: RunInput{ProductName: "shoe"},
	})
	if err != nil {
		t.Fatalf("Agent.Chat() error = %v", err)
	}
	if reply.Operation == nil || reply.Operation.Type != OperationRun {
		t.Fatalf("operation = %#v, want run", reply.Operation)
	}
	_, run, err := application.Runner.ConfirmOperation(context.Background(), reply.Operation.ID)
	if err != nil {
		t.Fatalf("ConfirmOperation() error = %v", err)
	}
	if run == nil || run.ProjectID != "new-project" {
		t.Fatalf("run = %#v, want new-project", run)
	}
}

func TestAgentChatIsIdempotent(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()
	handler := NewHTTPHandler(application)

	chat := func(text string) AgentChatReply {
		request := httptest.NewRequest(http.MethodPost, "/agent/chat", bytes.NewBufferString(`{"project_id":"demo","text":"`+text+`","product_name":"shoe"}`))
		request.Header.Set("Idempotency-Key", "chat-1")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("POST /agent/chat status = %d: %s", response.Code, response.Body.String())
		}
		var reply AgentChatReply
		if err := json.NewDecoder(response.Body).Decode(&reply); err != nil {
			t.Fatalf("decode reply: %v", err)
		}
		return reply
	}
	first := chat("开始生成")
	second := chat("开始生成")
	if first.Messages[0].ID != second.Messages[0].ID || first.Operation.ID != second.Operation.ID {
		t.Fatalf("idempotent replies differ: first=%#v second=%#v", first, second)
	}

	request := httptest.NewRequest(http.MethodPost, "/agent/chat", bytes.NewBufferString(`{"project_id":"demo","text":"不同请求"}`))
	request.Header.Set("Idempotency-Key", "chat-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("reused key with different input status = %d", response.Code)
	}
}

func TestHTTPReturnsLatestProjectSession(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()

	reply, err := application.Agent.Chat(context.Background(), AgentChatInput{ProjectID: "demo", Text: "开始生成", RunInput: RunInput{ProductName: "shoe"}})
	if err != nil {
		t.Fatalf("Agent.Chat() error = %v", err)
	}
	_, run, err := application.Runner.ConfirmOperation(context.Background(), reply.Operation.ID)
	if err != nil {
		t.Fatalf("ConfirmOperation() error = %v", err)
	}

	response := httptest.NewRecorder()
	NewHTTPHandler(application).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/projects/demo/session", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET project session status = %d", response.Code)
	}
	var session ProjectSession
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
		t.Fatalf("decode project session: %v", err)
	}
	if session.Conversation == nil || session.Conversation.ID != reply.Conversation.ID || session.RunID != run.ID {
		t.Fatalf("project session = %#v", session)
	}
}

func TestLocalAgentProposesCanvasEdit(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()

	reply, err := application.Agent.Chat(context.Background(), AgentChatInput{
		ProjectID: "demo",
		Text:      "新增一个预览节点",
	})
	if err != nil {
		t.Fatalf("Agent.Chat() error = %v", err)
	}
	if reply.Operation == nil || reply.Operation.Type != OperationAddNode {
		t.Fatalf("operation = %#v, want add_node", reply.Operation)
	}
	if _, _, err := application.Runner.ConfirmOperation(context.Background(), reply.Operation.ID); err != nil {
		t.Fatalf("ConfirmOperation() error = %v", err)
	}
	project, err := application.Store.GetProject(context.Background(), "demo")
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	latest := project.WorkflowVersions[len(project.WorkflowVersions)-1]
	found := false
	for _, node := range latest.Nodes {
		if node.Kind == PreviewNode && node.ID != "preview" {
			found = true
		}
	}
	if !found {
		t.Fatalf("confirmed workflow does not contain the added preview node: %#v", latest.Nodes)
	}
}

func TestLocalAgentProposesRunCancellation(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()

	reply, err := application.Agent.Chat(context.Background(), AgentChatInput{
		ProjectID: "demo",
		RunID:     "run-1",
		Text:      "取消当前运行",
	})
	if err != nil {
		t.Fatalf("Agent.Chat() error = %v", err)
	}
	if reply.Operation == nil || reply.Operation.Type != OperationCancel || reply.Operation.RunID != "run-1" {
		t.Fatalf("operation = %#v, want cancel run-1", reply.Operation)
	}
	operation, err := application.Store.GetOperation(context.Background(), reply.Operation.ID)
	if err != nil {
		t.Fatalf("GetOperation() error = %v", err)
	}
	if operation.Status != OperationPending {
		t.Fatalf("operation status = %s, want pending", operation.Status)
	}
}

func TestLocalAgentReadsSelectedWorkflowVersion(t *testing.T) {
	store := NewStore(t.TempDir() + "/workflow.json")
	project := Project{
		ID:                     "demo",
		CurrentWorkflowVersion: "selected",
		WorkflowVersions: []WorkflowVersion{
			{ID: "selected", Workflow: Workflow{Nodes: []WorkflowNode{{ID: "selected-node", Kind: PreviewNode}}}},
			{ID: "newer-but-not-selected", Workflow: Workflow{Nodes: []WorkflowNode{{ID: "other-node", Kind: FinalVideoNode}}}},
		},
	}
	if err := store.SaveProject(context.Background(), project); err != nil {
		t.Fatalf("SaveProject() error = %v", err)
	}
	agent, err := NewCanvasAgent(nil, store)
	if err != nil {
		t.Fatalf("NewCanvasAgent() error = %v", err)
	}

	reply, err := agent.Chat(context.Background(), AgentChatInput{ProjectID: "demo", Text: "删除 selected-node"})
	if err != nil {
		t.Fatalf("Agent.Chat() error = %v", err)
	}
	if reply.Operation == nil || reply.Operation.TargetNodeID != "selected-node" {
		t.Fatalf("operation = %#v, want selected-node from current workflow", reply.Operation)
	}
}

func TestModelAgentReturnsAfterFirstCanvasOperation(t *testing.T) {
	store := NewStore(t.TempDir() + "/workflow.json")
	if err := EnsureProject(context.Background(), store, "demo"); err != nil {
		t.Fatalf("EnsureProject() error = %v", err)
	}
	chatModel := &canvasOperationModel{}
	agent, err := NewCanvasAgent(chatModel, store)
	if err != nil {
		t.Fatalf("NewCanvasAgent() error = %v", err)
	}

	reply, err := agent.Chat(context.Background(), AgentChatInput{ProjectID: "demo", Text: "开始生成广告", RunInput: RunInput{ProductName: "shoe"}})
	if err != nil {
		t.Fatalf("Agent.Chat() error = %v", err)
	}
	if chatModel.calls != 1 {
		t.Fatalf("model calls = %d, want 1", chatModel.calls)
	}
	if reply.Operation == nil || reply.Operation.Type != OperationRun {
		t.Fatalf("operation = %#v, want run", reply.Operation)
	}
	input, err := decode[RunInput](reply.Operation.Payload)
	if err != nil || input.ProductName != "shoe" {
		t.Fatalf("operation run input = %#v, %v", input, err)
	}
	if _, err := store.GetOperation(context.Background(), reply.Operation.ID); err != nil {
		t.Fatalf("GetOperation() error = %v", err)
	}
}

type canvasOperationModel struct {
	calls int
}

func (model *canvasOperationModel) Generate(context.Context, []*schema.Message, ...modelcomponent.Option) (*schema.Message, error) {
	model.calls++
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
		ID:   "call-1",
		Type: "function",
		Function: schema.FunctionCall{
			Name:      "propose_canvas_operation",
			Arguments: `{"type":"run"}`,
		},
	}}}, nil
}

func (model *canvasOperationModel) Stream(ctx context.Context, input []*schema.Message, options ...modelcomponent.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := model.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func (model *canvasOperationModel) WithTools([]*schema.ToolInfo) (modelcomponent.ToolCallingChatModel, error) {
	return model, nil
}
