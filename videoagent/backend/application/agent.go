package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

const canvasAgentSystemPrompt = `你是广告视频画布助手。
你可以查看当前工作流，并提出修改节点、连线或运行工作流的操作。
任何会改变画布或启动任务的行为都必须先调用 propose_canvas_operation，返回待确认操作，不能直接执行。
回答要简洁，明确告诉用户当前操作是否需要确认。`

type CanvasAgent struct {
	model         model.ToolCallingChatModel
	store         *Store
	maxIterations int
}

type ChatAgent interface {
	Chat(context.Context, AgentChatInput) (AgentChatReply, error)
}

type canvasOperationArgs struct {
	Type         string          `json:"type" jsonschema:"required,description=操作类型: add_node/delete_node/connect/update_node/update_input/run/retry/cancel"`
	TargetNodeID string          `json:"target_node_id,omitempty" jsonschema:"description=目标节点 ID"`
	RunID        string          `json:"run_id,omitempty" jsonschema:"description=重试操作对应的 Run ID"`
	Payload      json.RawMessage `json:"payload,omitempty" jsonschema:"description=操作参数 JSON"`
}

func NewCanvasAgent(chatModel model.BaseChatModel, store *Store) (*CanvasAgent, error) {
	if store == nil {
		return nil, fmt.Errorf("agent store is nil")
	}
	if chatModel == nil {
		return &CanvasAgent{store: store, maxIterations: 8}, nil
	}
	toolModel, ok := chatModel.(model.ToolCallingChatModel)
	if !ok {
		return nil, fmt.Errorf("canvas agent model does not support tool calling")
	}
	return &CanvasAgent{model: toolModel, store: store, maxIterations: 8}, nil
}

func (agent *CanvasAgent) Chat(ctx context.Context, input AgentChatInput) (reply AgentChatReply, err error) {
	if input.ProjectID == "" || strings.TrimSpace(input.Text) == "" {
		return reply, fmt.Errorf("project id and text are required")
	}
	if err = EnsureProject(ctx, agent.store, input.ProjectID); err != nil {
		return reply, err
	}
	if cached, oldText, found, err := agent.store.FindAgentChatReply(ctx, input.ProjectID, input.IdempotencyKey); err != nil {
		return reply, err
	} else if found {
		if oldText != input.Text {
			return reply, fmt.Errorf("idempotency key was already used with a different message")
		}
		return cached, nil
	}
	conversation, err := agent.conversation(ctx, input)
	if err != nil {
		return reply, err
	}
	user := Message{
		ID: idempotentChatID("message", input, "user"), ConversationID: conversation.ID,
		IdempotencyKey: input.IdempotencyKey, Role: "user",
		Parts: []MessagePart{{Type: "text", Text: input.Text}}, CreatedAt: time.Now().UTC(),
	}
	if err := agent.store.SaveMessage(ctx, user); err != nil {
		return reply, err
	}

	if agent.model == nil {
		reply, err = agent.localReply(ctx, input, conversation)
	} else {
		reply, err = agent.react(ctx, input, conversation)
	}
	if err != nil {
		return reply, err
	}
	return reply, nil
}

func (agent *CanvasAgent) conversation(ctx context.Context, input AgentChatInput) (Conversation, error) {
	if input.ConversationID != "" {
		conversation, err := agent.store.GetConversation(ctx, input.ConversationID)
		if err != nil {
			return Conversation{}, err
		}
		if conversation.ProjectID != input.ProjectID {
			return Conversation{}, fmt.Errorf("conversation does not belong to project: %s", input.ProjectID)
		}
		return conversation, nil
	}
	conversation := Conversation{ID: newID("conversation"), ProjectID: input.ProjectID, CreatedAt: time.Now().UTC()}
	if err := agent.store.SaveConversation(ctx, conversation); err != nil {
		return Conversation{}, err
	}
	return conversation, nil
}

func (agent *CanvasAgent) localReply(ctx context.Context, input AgentChatInput, conversation Conversation) (AgentChatReply, error) {
	text := strings.ToLower(input.Text)
	if operation, reply, found := agent.localCanvasOperation(ctx, input, text); found {
		return agent.createOperationReply(ctx, input, conversation, operation, reply)
	}
	if !strings.Contains(text, "运行") && !strings.Contains(text, "开始") && !strings.Contains(text, "生成") && !strings.Contains(text, "run") {
		return agent.saveReply(ctx, input, conversation, "我可以帮你运行流程，也可以根据你的描述修改节点和连线。", nil)
	}
	payload, err := json.Marshal(RunInput{ProductName: input.ProductName, ProductImageURLs: input.ProductImageURLs, Brief: input.Brief})
	if err != nil {
		return AgentChatReply{}, err
	}
	operation := CanvasOperation{ProjectID: input.ProjectID, IdempotencyKey: input.IdempotencyKey, Type: OperationRun, Payload: payload}
	return agent.createOperationReply(ctx, input, conversation, operation, "已经准备好运行当前工作流，请确认后开始。")
}

func (agent *CanvasAgent) localCanvasOperation(ctx context.Context, input AgentChatInput, text string) (CanvasOperation, string, bool) {
	project, err := agent.store.GetProject(ctx, input.ProjectID)
	if err != nil || len(project.WorkflowVersions) == 0 {
		return CanvasOperation{}, "", false
	}
	version, err := currentWorkflow(project)
	if err != nil {
		return CanvasOperation{}, "", false
	}
	workflow := version.Workflow
	if strings.Contains(text, "删除") {
		for _, node := range workflow.Nodes {
			if nodeMentioned(text, node) {
				payload, _ := json.Marshal(map[string]string{"node_id": node.ID})
				return CanvasOperation{ProjectID: input.ProjectID, Type: OperationDeleteNode, TargetNodeID: node.ID, Payload: payload}, "已经准备好删除节点，请确认。", true
			}
		}
	}
	if strings.Contains(text, "添加") || strings.Contains(text, "新增") || strings.Contains(text, "拖入") {
		kind := localNodeKind(text)
		if kind != "" {
			node := WorkflowNode{ID: string(kind) + "-" + newID("node"), Kind: kind}
			payload, _ := json.Marshal(node)
			return CanvasOperation{ProjectID: input.ProjectID, Type: OperationAddNode, Payload: payload}, "已经准备好添加节点，请确认。", true
		}
	}
	if strings.Contains(text, "连接") || strings.Contains(text, "连线") {
		from, to := localNodePair(text, workflow.Nodes)
		if from != nil && to != nil {
			catalog := defaultNodeCatalog()
			edge := WorkflowEdge{
				FromNodeID: from.ID,
				FromPort:   firstPortName(catalog[from.Kind].Outputs),
				ToNodeID:   to.ID,
				ToPort:     firstPortName(catalog[to.Kind].Inputs),
			}
			payload, _ := json.Marshal(edge)
			return CanvasOperation{ProjectID: input.ProjectID, Type: OperationConnect, Payload: payload}, "已经准备好连接节点，请确认。", true
		}
	}
	if input.RunID != "" {
		payload, _ := json.Marshal(map[string]string{"run_id": input.RunID})
		if strings.Contains(text, "重试") || strings.Contains(text, "retry") {
			return CanvasOperation{ProjectID: input.ProjectID, RunID: input.RunID, Type: OperationRetry, Payload: payload}, "已经准备好重试当前运行，请确认。", true
		}
		if strings.Contains(text, "取消") || strings.Contains(text, "停止") || strings.Contains(text, "cancel") {
			return CanvasOperation{ProjectID: input.ProjectID, RunID: input.RunID, Type: OperationCancel, Payload: payload}, "已经准备好取消当前运行，请确认。", true
		}
	}
	return CanvasOperation{}, "", false
}

func localNodeKind(text string) NodeKind {
	for _, candidate := range localNodeAliases {
		for _, word := range candidate.aliases {
			if strings.Contains(text, word) {
				return candidate.kind
			}
		}
	}
	return ""
}

var localNodeAliases = []struct {
	kind    NodeKind
	aliases []string
}{
	{RequirementNode, []string{"需求分析", "需求"}},
	{ClipScriptNode, []string{"分镜脚本", "分镜"}},
	{CompetitionReferenceNode, []string{"竞品图", "竞品"}},
	{PromptTTSNode, []string{"tts", "配音", "音频"}},
	{CharacterReferenceNode, []string{"人物参考", "人物图"}},
	{PreviewNode, []string{"预览", "粗剪"}},
	{FinalVideoNode, []string{"成片", "正式视频"}},
}

func localNodePair(text string, nodes []WorkflowNode) (*WorkflowNode, *WorkflowNode) {
	var matched []*WorkflowNode
	for index := range nodes {
		node := &nodes[index]
		if nodeMentioned(text, *node) {
			matched = append(matched, node)
		}
	}
	if len(matched) < 2 {
		return nil, nil
	}
	return matched[0], matched[1]
}

func nodeMentioned(text string, node WorkflowNode) bool {
	if strings.Contains(text, strings.ToLower(node.ID)) || strings.Contains(text, node.ID) {
		return true
	}
	for _, candidate := range localNodeAliases {
		if candidate.kind != node.Kind {
			continue
		}
		for _, alias := range candidate.aliases {
			if strings.Contains(text, alias) {
				return true
			}
		}
	}
	return false
}

func firstPortName(ports []PortDefinition) string {
	if len(ports) == 0 {
		return ""
	}
	return ports[0].Name
}

func (agent *CanvasAgent) react(ctx context.Context, input AgentChatInput, conversation Conversation) (AgentChatReply, error) {
	tools, err := agent.tools(input)
	if err != nil {
		return AgentChatReply{}, err
	}
	messages := []*schema.Message{schema.SystemMessage(canvasAgentSystemPrompt)}
	history, err := agent.store.ListMessages(ctx, conversation.ID)
	if err != nil {
		return AgentChatReply{}, err
	}
	for _, item := range history {
		content := messageText(item)
		if content == "" {
			continue
		}
		switch item.Role {
		case "assistant":
			messages = append(messages, schema.AssistantMessage(content, nil))
		default:
			messages = append(messages, schema.UserMessage(content))
		}
	}
	reactRun, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: agent.model,
		ToolsConfig:      compose.ToolsNodeConfig{Tools: tools},
		ToolReturnDirectly: map[string]struct{}{
			"propose_canvas_operation": {},
		},
		MaxStep:   agent.maxIterations*2 + 1,
		GraphName: "CanvasAgent",
	})
	if err != nil {
		return AgentChatReply{}, err
	}
	msg, err := reactRun.Generate(ctx, messages)
	if err != nil {
		return AgentChatReply{}, err
	}
	if msg == nil {
		return AgentChatReply{}, fmt.Errorf("agent model returned nil message")
	}
	if msg.Role != schema.Tool || msg.ToolName != "propose_canvas_operation" {
		return agent.saveReply(ctx, input, conversation, msg.Content, nil)
	}
	var operation CanvasOperation
	if err := json.Unmarshal([]byte(msg.Content), &operation); err != nil {
		return AgentChatReply{}, fmt.Errorf("decode proposed canvas operation: %w", err)
	}
	return agent.saveReply(ctx, input, conversation, "已经准备好画布操作，请确认后执行。", &operation)
}

func messageText(message Message) string {
	parts := make([]string, 0, len(message.Parts))
	for _, part := range message.Parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (agent *CanvasAgent) tools(input AgentChatInput) ([]tool.BaseTool, error) {
	projectID := input.ProjectID
	flowTool, err := utils.InferTool("get_canvas_workflow", "读取当前画布工作流。", func(ctx context.Context, _ struct{}) (string, error) {
		project, err := agent.store.GetProject(ctx, projectID)
		if err != nil {
			return "", err
		}
		version, err := currentWorkflow(project)
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(version)
		return string(data), err
	})
	if err != nil {
		return nil, err
	}
	opTool, err := utils.InferTool("propose_canvas_operation", "提出一个需要用户确认的画布操作。", func(ctx context.Context, args canvasOperationArgs) (string, error) {
		if !validOperationType(args.Type) {
			return "", fmt.Errorf("unsupported canvas operation: %s", args.Type)
		}
		payload := args.Payload
		if args.Type == OperationRun && len(payload) == 0 {
			var err error
			payload, err = json.Marshal(input.RunInput)
			if err != nil {
				return "", err
			}
		}
		operation := CanvasOperation{
			ID: newID("operation"), ProjectID: projectID, RunID: args.RunID,
			IdempotencyKey: input.IdempotencyKey, SourceMessageID: idempotentChatID("message", input, "assistant"),
			Type: args.Type, TargetNodeID: args.TargetNodeID, Payload: payload,
			Status: OperationPending, CreatedAt: time.Now().UTC(),
		}
		stored, _, err := agent.store.CreateOrGetOperation(ctx, operation)
		if err != nil {
			return "", err
		}
		return string(mustJSON(stored)), nil
	})
	if err != nil {
		return nil, err
	}
	return []tool.BaseTool{flowTool, opTool}, nil
}

func (agent *CanvasAgent) createOperationReply(ctx context.Context, input AgentChatInput, conversation Conversation, operation CanvasOperation, text string) (AgentChatReply, error) {
	operation.ID = newID("operation")
	operation.IdempotencyKey = input.IdempotencyKey
	operation.SourceMessageID = idempotentChatID("message", input, "assistant")
	operation.Status = OperationPending
	operation.CreatedAt = time.Now().UTC()
	stored, _, err := agent.store.CreateOrGetOperation(ctx, operation)
	if err != nil {
		return AgentChatReply{}, err
	}
	return agent.saveReply(ctx, input, conversation, text, &stored)
}

func (agent *CanvasAgent) saveReply(ctx context.Context, input AgentChatInput, conversation Conversation, text string, operation *CanvasOperation) (AgentChatReply, error) {
	parts := []MessagePart{{Type: "text", Text: text}}
	messageID := idempotentChatID("message", input, "assistant")
	if operation != nil {
		parts = append(parts, MessagePart{Type: "operation", OperationID: operation.ID})
		if operation.SourceMessageID != "" {
			messageID = operation.SourceMessageID
		}
	}
	message := Message{ID: messageID, ConversationID: conversation.ID, IdempotencyKey: input.IdempotencyKey, Role: "assistant", Parts: parts, CreatedAt: time.Now().UTC()}
	if err := agent.store.SaveMessage(ctx, message); err != nil {
		return AgentChatReply{}, err
	}
	return AgentChatReply{Conversation: conversation, Messages: []Message{message}, Operation: operation}, nil
}

func idempotentChatID(prefix string, input AgentChatInput, role string) string {
	if input.IdempotencyKey == "" {
		return newID(prefix)
	}
	digest := sha256.Sum256([]byte(input.ProjectID + ":" + input.IdempotencyKey + ":" + role))
	return fmt.Sprintf("%s-%x", prefix, digest[:12])
}

func mustJSON(value any) []byte {
	payload, _ := json.Marshal(value)
	return payload
}
