# Canvas Video Agent 技术方案

状态：现行方案。本文是 Canvas Agent 一期方案和本地视频执行器方案的唯一合并版本。`/Users/bytedance/go/src/content/creative_aic_agent/canvas_agent/TECHNICAL_DESIGN.md` 仅保留为一期资源准备阶段的历史档案，不再作为实现依据。

## 1. 目标与边界

系统让用户在画布上查看、编辑并运行视频生产流程。每次运行固化当时的工作流版本；产物之间的依赖关系由 `Artifact.ParentIDs` 表示，前端据此绘制来源连线。

```text
Canvas Project + WorkflowVersion
        |
        +-> Agent: 理解用户意图，提出画布或运行操作
        |
        +-> DAG Runner: 执行已确认的工作流，恢复异步节点
```

本期提供一个可运行的内置视频模板，从需求分析走到 `finalvideo`。编排层只依赖模型、图片、TTS 和视频能力接口；生产实现由部署方注入底层 Client，不调用 Mega 的 `BatchSaveClipScript`、`GenPictureClipCandidates`、`MixCutByClipScript` 工作流接口，也不迁移 Mega 的 Task、SubTask、ASL、FSM、配额或审核编排壳。

本地默认使用 MongoDB 保存 Run、Workflow、Artifact、Operation 和 Job 状态，使用 NATS JetStream 持久化异步完成消息；传入 `-mongo-uri ""` 时才回退到 JSON 文件。远端能力模式复用同一套状态和消息协议，DAG、Run、节点状态和恢复规则不变。

## 2. Canvas、Agent 与 DAG 的边界

| 层 | 负责什么 | 不负责什么 |
|---|---|---|
| Canvas | 编辑节点和连线，展示 Run、节点状态、Artifact 来源 | 不直接调用模型或生成服务 |
| Agent / ReAct | 将用户请求转换为画布操作、参数修改、运行或重试建议 | 不推进异步任务，不决定节点是否可运行 |
| DAG Runner | 按依赖领取节点、持久化提交、处理回调/轮询、生成 Artifact | 不理解自然语言，不修改未确认的工作流 |
| Node Client | 调用模型、图片、TTS、视频等原子能力 | 不保存 DAG 状态，不创建下游节点 |

Agent 可以提出 `add_node`、`connect`、`update_input`、`run`、`retry` 等 Action；画布确认后生成新的 `WorkflowVersion`。Runner 永远只执行 Run 内嵌的不可变版本。因此 Agent 的 ReAct loop 与 DAG 不嵌套：前者负责决策，后者负责确定性编排、持久化和恢复。

## 3. 页面与运行模型

![Canvas Video Agent 产品布局](assets/canvas_video_agent_product.png)

实线表示工作流依赖；Artifact 来源线以 `ParentIDs` 为准。例如某个预览视频同时连到 `clipscript`、竞品图、TTS 和人物参考图，`finalvideo` 连到它采用的预览视频。这样画布能回答“这个视频来自哪段分镜和哪些原料”，而不是只展示平铺卡片。

每个工作流节点直接展示自己的成功产物：`requirement` 和 `clipscript` 展示文本，图片节点展示图片，`prompt_tts` 展示音频播放器，`preview` 与 `finalvideo` 展示视频播放器。一个控制节点产生多个子任务时，节点内按子任务顺序展示全部产物；本地确定性 Client 使用明确的媒体占位，真实 Client 返回 HTTP URL 后直接渲染媒体。

Canvas 服务拥有 Project 和可编辑的 WorkflowVersion；执行器只接收 `ProjectID` 和一份不可变的 WorkflowVersion 快照：

```go
type Workflow struct {
	Nodes []WorkflowNode `json:"nodes"`
	Edges []WorkflowEdge `json:"edges"`
}

type WorkflowVersion struct {
	ID        string `json:"workflow_version_id"`
	ProjectID string `json:"project_id"`
	Revision  int    `json:"revision"`
	Workflow
}

type Run struct {
	ID        string          `json:"run_id"`
	ProjectID string          `json:"project_id"`
	Workflow  WorkflowVersion `json:"workflow"`
	Input     RunInput        `json:"input"`
	NodeRuns  []NodeRun       `json:"node_runs"`
}
```

当前代码已实现 Run 快照、Project/Workflow API、画布编辑器、Agent/ReAct 操作确认和自定义 Workflow 校验入口。Agent 的远端模型、图片、TTS、预览和成片 Client 通过配置注入；不配置时使用本地确定性 Client 完成本地闭环。

### 3.1 右侧会话与确认操作

右侧对话不是 Canvas 节点的一部分。`Message` 保存用户和 Agent 可见的内容；`CanvasOperation` 保存 Agent 提出的、会改变画布或 Run 的实际操作。两者通过 `MessagePart` 关联：一条 Agent 消息既可以有普通文字，也可以引用一个产物和一个待确认操作。

```go
type Conversation struct {
	ID        string    `json:"conversation_id"`
	ProjectID string    `json:"project_id"`
	CreatedAt time.Time `json:"created_at"`
}

type Message struct {
	ID             string        `json:"message_id"`
	ConversationID string        `json:"conversation_id"`
	Role           string        `json:"role"` // user / assistant / system
	Parts          []MessagePart `json:"parts"`
	CreatedAt      time.Time     `json:"created_at"`
}

type MessagePart struct {
	Type        string `json:"type"` // text / artifact / operation
	Text        string `json:"text,omitempty"`
	ArtifactID  string `json:"artifact_id,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
}

type CanvasOperation struct {
	ID              string          `json:"operation_id"`
	ProjectID       string          `json:"project_id"`
	RunID           string          `json:"run_id,omitempty"`
	IdempotencyKey  string          `json:"idempotency_key,omitempty"`
	SourceMessageID string          `json:"source_message_id"`
	Type            string          `json:"type"` // update_node / connect / run / retry / cancel
	TargetNodeID    string          `json:"target_node_id,omitempty"`
	Payload         json.RawMessage `json:"payload"`
	Status          string          `json:"status"` // pending / confirmed / applied / rejected
	CreatedAt       time.Time       `json:"created_at"`
}
```

```text
Conversation
  -> Message("已生成 clipscript，请确认运行预览")
       -> MessagePart{text}
       -> MessagePart{artifact: clipscript-v1}
       -> MessagePart{operation: run-preview-001}
  -> CanvasOperation{type: run, target: preview, status: pending}
```

规则如下：

1. 普通问答、解释和运行进度只创建 `Message`，不创建 `CanvasOperation`。
2. Agent 提议修改节点、连线、输入，或运行/重试/取消某个节点时，先持久化 `CanvasOperation{Status: pending}`，再把它引用到 Agent 消息中。
3. 用户确认后，服务原子地把 Operation 标记为 `confirmed`，执行对应画布修改或创建 Run；成功后标记为 `applied`，拒绝则为 `rejected`。
4. 画布修改产生新的 `WorkflowVersion`；`run` / `retry` 只作用于指定 Run 或新建 Run，不能修改已运行版本。
5. SSE 的 token/delta 仅用于实时显示；持久化的是完成后的 `Message` 和 `CanvasOperation`，避免把传输分片当成业务对象。
6. HTTP 创建 Operation 和 Run 支持 `Idempotency-Key`；相同 Project + Key 直接返回第一次创建的 Operation，不重复启动 Run。

`Conversation` 不内嵌 `[]Message`，消息按 `ConversationID + CreatedAt` 独立分页查询。`CanvasOperation` 也不附着在 Message 的 JSON 中，因为它需要独立的确认、审计、幂等和重试状态。

## 4. 内置 VideoWorkflow 模板

`VideoWorkflow` 是默认模板，不是整个 Canvas 产品的同义词：

```text
requirement -> clipscript -> competition --+
                           -> tts ---------+-> preview -> finalvideo
                           -> character ---+
```

```go
func VideoWorkflow() Workflow {
	return Workflow{
		Nodes: []WorkflowNode{
			{ID: "requirement", Kind: RequirementNode},
			{ID: "clipscript", Kind: ClipScriptNode},
			{ID: "competition", Kind: CompetitionReferenceNode},
			{ID: "tts", Kind: PromptTTSNode},
			{ID: "character_reference", Kind: CharacterReferenceNode},
			{ID: "preview", Kind: PreviewNode},
			{ID: "finalvideo", Kind: FinalVideoNode},
		},
		Edges: []WorkflowEdge{
			{FromNodeID: "requirement", FromPort: "requirement", ToNodeID: "clipscript", ToPort: "requirement"},
			{FromNodeID: "clipscript", FromPort: "clipscript", ToNodeID: "competition", ToPort: "clipscript"},
			{FromNodeID: "clipscript", FromPort: "clipscript", ToNodeID: "tts", ToPort: "clipscript"},
			{FromNodeID: "clipscript", FromPort: "clipscript", ToNodeID: "character_reference", ToPort: "clipscript"},
			{FromNodeID: "clipscript", FromPort: "clipscript", ToNodeID: "preview", ToPort: "clipscript"},
			{FromNodeID: "competition", FromPort: "competition_reference_image", ToNodeID: "preview", ToPort: "resources"},
			{FromNodeID: "tts", FromPort: "voice_preview", ToNodeID: "preview", ToPort: "resources"},
			{FromNodeID: "character_reference", FromPort: "character_reference_image", ToNodeID: "preview", ToPort: "resources"},
			{FromNodeID: "preview", FromPort: "preview_video", ToNodeID: "finalvideo", ToPort: "preview_video"},
			{FromNodeID: "clipscript", FromPort: "clipscript", ToNodeID: "finalvideo", ToPort: "clipscript"},
		},
	}
}
```

节点目录由后端注册，用户只保存节点实例和参数，不保存可执行代码：

```go
type PortDefinition struct {
	Name         string
	ArtifactKind string
	Required     bool
}

type NodeDefinition struct {
	Kind    NodeKind
	Inputs  []PortDefinition
	Outputs []PortDefinition
}
```

开始运行时，Runner 接收用户编辑后的 `Workflow`，先按节点目录校验节点类型、端口类型、重复连线和环路，再复制成 `WorkflowVersion` 写入 Run。`POST /runs` 只创建待确认 Operation，确认后才使用当前 `WorkflowVersion` 创建 Run。当前执行器已经支持节点布局和自定义节点 ID，具体节点种类仍必须来自后端注册目录。

节点状态只使用 `pending`、`running`、`waiting`、`succeeded`、`failed`。资源控制节点的子任务允许部分失败：全部子任务终态后，控制节点记为 `succeeded`；失败资源保留失败 Artifact，但不阻塞预览。`requirement`、`clipscript`、`preview` 与 `finalvideo` 自身失败则使对应节点失败。

## 5. 节点输入、产物与原子能力

| 节点 | 直接能力 | 成功产物 | 关键规则 |
|---|---|---|---|
| `requirement` | Fornax `aic.aic_tool.user_req_analysis` | `requirement` | 输出目标、受众、卖点 |
| `clipscript` | 当前租户的分镜 Prompt；即创端到端为 `jichuang.creative.dr_script_e2e` | `clipscript` | 输出镜头、旁白、画面描述 |
| `competition_reference_image` | Fornax 规划 + `remote.Text2Image` | 图片和 `clipscript_annotation` | 镜头改写必须是独立 Artifact，不覆盖 clipscript |
| `prompt_tts` | Fornax 规划 + Matx PromptTTS/ZeroShot | 音频 | 生成音色参考、试听例句和分镜口播；试听例句用于画布试听，分镜口播用于成片 |
| `character_reference_image` | AIGCPlanning + `remote.Text2Image` | 审核通过的参考图 | 提示词审核、图片审核，允许一次模型 fallback |
| `preview` | 底层视频候选/预览能力 | `preview_video` | 读取 clipscript 和所有成功资源 |
| `finalvideo` | 底层正式视频生成能力 | `finalvideo` | 同时读取完整 clipscript 和指定预览视频 |

`clipscript` 是不可变版本产物。资源计划保存真实 `ParentArtifactID`，预览和 `finalvideo` 保存所有实际输入的 Artifact ID。ClipMix 计划同时保留每个候选在各个 cut 中的 `CutNumber/ItemIndex/CandidateIndex`；`finalvideo` 按 cut 拆成独立渲染任务，按 item、candidate 顺序组装视频，并根据目标画面时长计算裁剪区间与播放速度：

```go
type Artifact struct {
	ID        string          `json:"artifact_id"`
	Kind      string          `json:"kind"`
	Status    string          `json:"status"`
	ParentIDs []string        `json:"parent_ids,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Message   string          `json:"message,omitempty"`
}
```

因此产物关系来自真实输入，而不是前端根据节点名称猜测：竞品图/TTS/人物图可追溯到 `clipscript`，预览可追溯到这批资源，`finalvideo` 可追溯到完整 `clipscript` 和选中的预览。

## 6. 可恢复异步执行

Runner 只有三个公开动作：创建 Run、推进就绪节点、处理 MQ 消息。等待任务的查询和未知提交的对账都先进入 JetStream，不提供绕过 MQ 的 HTTP 推进入口。

```text
StartRun -> Advance
Advance  -> 领取 ready NodeRun -> 同步完成，或提交异步 Job 后 waiting
Callback -> 按 provider + job_id 找到 NodeRun -> Refresh -> Advance
Poller   -> 发布 poll:job_id / reconcile:submit_key 消息
Consumer -> 按 job_id 查询，或按 submit_key 对账 -> Refresh -> Advance
```

`NodeRun.SubmitKey` 在提交前持久化。提交超时或进程崩溃后，必须按该 key 查询已有 Job，禁止盲目重提：

```go
type NodeRun struct {
	NodeID            string    `json:"node_id"`
	Kind              NodeKind  `json:"kind"`
	InstanceKey       string    `json:"instance_key,omitempty"`
	State             NodeState `json:"state"`
	Provider          string    `json:"provider,omitempty"`
	JobID             string    `json:"job_id,omitempty"`
	SubmitKey         string    `json:"submit_key"`
	Attempt           int       `json:"attempt,omitempty"`
	ClaimToken        string    `json:"claim_token,omitempty"`
	ClaimedAt         *time.Time `json:"claimed_at,omitempty"`
	SubmitStarted     bool      `json:"submit_started,omitempty"`
	SubmissionUnknown bool      `json:"submission_unknown,omitempty"`
	Artifacts         []Artifact `json:"artifacts,omitempty"`
	FallbackSubmitted bool      `json:"fallback_submitted,omitempty"`
	Message           string    `json:"message,omitempty"`
}
```

恢复规则：

1. 提交异常或 `FindBySubmitKey` 暂时异常，节点维持 `waiting`，下一次继续对账。
2. Provider 明确不支持按 `submit_key` 对账时，节点记录 `submission_unknown=true` 并失败；普通重试拒绝清理该提交痕迹，避免重复创建下游任务。用户只能在确认原任务未创建后发起新的 Run。
3. 重启时只恢复没有活跃租约的 `running` 节点：同步节点回到 `pending`；`SubmitStarted=true` 的异步节点保持不可重新提交，由 `Advance` 先按 `job_id` 或 `submit_key` 对账，再进入 `waiting`。其他实例持有的未过期租约不被启动流程抢占。
4. 回调以 `provider:job_id:event_id` 去重，对账消息以 `provider:submit_key:event_id` 去重。回调早于 `job_id` 持久化时返回可重试错误，JetStream 不 ACK 并延迟重投；Job 落库后再刷新并推进 Run。
5. 人物参考图的 fallback 先保存 `FallbackSubmitted=true`，再在下一次推进时提交，因此最多提交一次。
6. Webhook/NATS consumer 只做鉴权、持久化和调度；不能在回调线程调用模型或再次提交任务。
7. 人工重试为失败实例递增 `Attempt`，生成新的 `submit_key:attempt-N`；旧 Submission 继续保留用于审计和对账，不跨 Mongo 集合删除旧凭证。
8. 服务启动逐个恢复 Run；单个损坏或暂时不可恢复的 Run 记录 `run_id` 和错误后继续，并进入后台恢复集合按轮询周期重试，成功后移出集合。恢复只领取持久化节点并对账，不能绕过 `SubmitKey` 重提任务，也不能阻止 MQ Consumer、Poller 和其他 Run 启动。
9. `pending/waiting` 节点在执行或刷新前持久化 `ClaimToken/ClaimedAt`，长时间提交和查询期间按租约的三分之一周期续租。其他实例只能在租约释放或过期后重新领取；`apply/requeue` 必须校验原领取 token，拒绝迟到实例覆盖新结果。续租失败会取消当前节点调用：未提交的同步节点回到 `pending`，已开始提交的异步节点进入 `waiting` 并只允许按 `job_id/submit_key` 对账，旧实例不能继续写入结果。Mongo Store 的单次 Load/Save 最长一分钟，避免心跳永久阻塞。
10. 远端已返回 `job_id` 而独立 Submission 收据暂时写失败时，Runner 仍把该 `job_id` 写回 NodeRun，不能把它降级成可重试的普通失败；如果 Run 同时保存失败，恢复路径只能对账或标记 `submission_unknown`，禁止盲目重提。
11. Provider 返回 nil error 时必须同时返回 `job_id` 或同步终态；两者都为空属于无效受理回执，节点记录 `submission_unknown=true` 并失败，不进入空 `job_id` 轮询，也不自动重提。
12. Project 初始化、默认工作流升级、画布保存和 Operation 确认都通过同一个 CAS 更新入口读取最新版本，不能把启动时读到的旧 Project 快照整体覆盖回 Mongo。
13. 取消先持久化 `cancel_requested` 并停止 DAG 推进，再取消所有已知远端 Job；任一 Provider 不支持取消或返回失败时，Run 保持 `cancel_requested` 并暴露错误。此时 Poller 和 MQ Callback 仍允许刷新已提交 Job，但不会启动任何后继节点；所有远端 Job 都进入终态后，未提交节点收敛为 `canceled`，Run 完成取消。远端取消全部成功时可以直接完成取消。

## 7. Client 契约与生产替换

编排层只依赖能力接口，不知道 Mega：

```go
type Planner interface {
	AnalyzeRequirement(context.Context, RunInput) (Requirement, error)
	CreateClipScript(context.Context, Requirement, RunInput) (ClipScript, error)
	PlanCompetition(context.Context, ClipScript, RunInput) ([]ResourcePlan, error)
	PlanTTS(context.Context, ClipScript) ([]ResourcePlan, error)
	PlanCharacterReferences(context.Context, ClipScript, RunInput) ([]ResourcePlan, error)
}

type PromptExecutor interface {
	Execute(context.Context, PromptRequest) (string, error)
}

type VideoClient interface {
	SubmitPreview(context.Context, VideoRequest) (SubmittedJob, error)
	GetPreview(context.Context, string) (JobStatus, error)
	FindPreviewBySubmitKey(context.Context, string) (SubmittedJob, bool, error)

	SubmitFinalVideo(context.Context, VideoRequest) (SubmittedJob, error)
	GetFinalVideo(context.Context, string) (JobStatus, error)
	FindFinalVideoBySubmitKey(context.Context, string) (SubmittedJob, bool, error)
}
```

具备下游取消契约时，`ImageClient`、`TTSClient`、`VideoClient` 可额外实现对应的 `Cancel` 方法；没有该能力时，系统不会把本地取消伪装成远程任务已取消。

异步回调的边界也由 Application 注入，而不是由 Runner 猜测消息系统：

```go
type CallbackVerifier interface {
	Verify(context.Context, string, []byte, http.Header) error
}

type MessagePublisher interface {
	Publish(context.Context, CallbackMessage) error
}

type MessageConsumer interface {
	Consume(context.Context, func(context.Context, CallbackMessage) error) error
}
```

HTTP 回调只负责读取原始 body、验签并发布 `CallbackMessage`；它不直接推进 DAG。入口同时兼容通用 `job_id` 和 provider 的 `task_id`，没有 `event_id` 时用原始 body 摘要生成幂等事件键。NATS durable consumer 收到消息后才调用 `Runner.OnCallback`。本地能力任务完成后也发布同一种消息，因此回调与本地模拟任务共用一条恢复路径。`Runner` 只在任务刷新和后继节点推进成功后写入回调收据；如果回调早于 Job 落库或刷新暂时失败，消息会留在 JetStream 中重投。

生产 Adapter 的职责是协议转换：`PromptPlanner` 根据节点构造业务变量，`PromptExecutor` 只负责按 PromptKey 执行一次 Prompt。需求分析使用 `aic.aic_tool.user_req_analysis`，clipscript 使用 `jichuang.creative.dr_script_e2e`，竞品图、TTS 和人物参考图分别使用已确认的 Prompt 契约。需求分析和 clipscript 的变量名由部署配置中的 `bindings` 显式映射，代码不猜测 TCC 变量名。Image/TTS/Video Adapter 直接调用部署方注入的底层 Client，传入 Agent 自己的业务身份、回调身份和 `submit_key`。它们不得转调 Mega 或 Gen 工作流接口。

上线前必须取得并验证：Agent 的 `BizId`、回调 Topic/Endpoint 和验签身份、按 `job_id` 查询终态的接口、按 `submit_key` 查询已提交任务的接口，以及取消契约。缺失任一项时生产 Client 不应启用。

## 8. 当前本地实现

代码位于 LLM 仓库，提供一条独立可启动的模板执行链路：

| 文件 | 责任 |
|---|---|
| `videoagent/types.go` | VideoWorkflow、Run、Artifact、节点类型 |
| `videoagent/workflow.go` | 节点目录、端口校验、环路校验和 Workflow 快照复制 |
| `videoagent/runner.go` | Start、Advance、MQ Callback |
| `videoagent/handler.go` | 需求、clipscript、资源、预览、finalvideo 节点 |
| `videoagent/store.go` | JSON/Mongo 持久化、任务认领和回调幂等收据 |
| `videoagent/local.go` | 本地 Job 库、队列及确定性 Client |
| `videoagent/mongo_application.go` | 使用 MongoDB 状态的本地确定性应用 |
| `videoagent/agent.go` | Eino ReAct 对话、Canvas Operation 提议和确认入口 |
| `videoagent/model_planner.go` | 用注入 Chat Model 生成需求、分镜和资源计划 |
| `videoagent/prompt_planner.go` | 使用真实 PromptKey 构造需求、clipscript、竞品图、TTS 和人物参考图计划 |
| `videoagent/fornax_prompt.go` | `PromptExecutor` 的 Fornax SDK 实现 |
| `videoagent/remote.go` | 直接能力 HTTP Client；不调用 Mega 工作流 |
| `videoagent/queue.go` | MQ 消息到 Run 回调恢复的适配边界 |
| `videoagent/nats.go` | NATS JetStream 持久发布、消费确认、失败重投和消息去重 |
| `videoagent/callback_auth.go` | 本地放行校验和生产 HMAC 校验实现 |
| `videoagent/monitor.go` | 节点执行、回调和错误的观测事件及本地计数器 |
| `videoagent/http.go` | Canvas、Agent、Operation、Run、回调 HTTP API |
| `videoagent/workflow_test.go` | 非法端口、环路和自定义 Workflow 快照测试 |
| `cmd/videoagent/main.go` | 可启动入口 |

```bash
brew install nats-server
nats-server -js -sd /opt/homebrew/var/video-agent-nats -a 127.0.0.1 -p 4222

GOTOOLCHAIN=go1.25.0 go run ./cmd/videoagent \
  -addr 127.0.0.1:18080 -data /tmp/video-agent
```

本机已经安装 MongoDB 时，使用 Mongo 状态启动：

```bash
GOTOOLCHAIN=go1.25.0 go run ./cmd/videoagent \
  -addr 127.0.0.1:18080 -data /tmp/video-agent \
  -mongo-uri mongodb://127.0.0.1:27017
```

本地启动前可用以下命令确保 MongoDB 监听 `127.0.0.1:27017`：

```bash
mongod --dbpath /opt/homebrew/var/mongodb \
  --logpath /opt/homebrew/var/log/mongodb/mongo.log \
  --fork --bind_ip 127.0.0.1 --port 27017
```

入口默认连接 `mongodb://127.0.0.1:27017`，数据库为 `video_agent`，状态集合为 `workflow_state`；需要使用 JSON 文件时传入 `-mongo-uri ""`。

```text
POST /runs                         # 返回 202 + pending Operation
POST /operations/{operation_id}/confirm
GET  /runs/{run_id}
GET  /healthz
GET  /workflow/node-definitions
GET  /metrics
```

本地实现使用 `LocalClients` 执行确定性能力任务，任务终态通过 NATS JetStream 发布和恢复，不直接调用 Runner。JetStream 使用文件存储、durable consumer、显式 ACK、消费心跳、失败 NAK 重投和消息去重；Runner 的回调收据提供第二层幂等保护。真实 callback 连续失败 20 次后进入同一 Stream 的 `.dead` Subject；后台查询和 submit-key 对账消息不进入 DLQ，而是在任务终态前持续重试。浏览器只调用 `GET /runs/{run_id}` 读取 Mongo，不能刷新远端任务或推进 DAG。`CanvasAgent` 在配置了 OpenAI-compatible Chat Model 时执行 Eino ReAct；未配置模型时保留本地可运行的最小操作解析器，保证本地链路可验收但不替代生产模型。

`Runner` 通过 `Monitor` 发送 `node_started`、`node_waiting`、`node_completed`、`node_failed` 和 `callback` 事件；`running/waiting` 不再被误计为完成。Monitor 只负责观测，不参与节点决策。默认内置进程内计数器，并由 `GET /metrics` 返回当前进程累计值。生产环境可以通过 `SetMonitor` 接入日志、指标或 Trace 系统，同时保留内置计数器用于健康检查和本地验收。

远端启动方式：

```bash
GOTOOLCHAIN=auto go run ./cmd/videoagent \
  -addr 127.0.0.1:18080 -data /tmp/video-agent \
  -remote-config ./configs/videoagent/remote.example.json \
  -model-config ./configs/videoagent/model.example.json \
  -prompt-config ./configs/videoagent/prompt.example.json
```

带 MongoDB 的远端启动方式：

```bash
GOTOOLCHAIN=auto go run ./cmd/videoagent \
  -remote-config ./configs/videoagent/remote.example.json \
  -model-config ./configs/videoagent/model.example.json \
  -prompt-config ./configs/videoagent/prompt.example.json \
  -mongo-uri "$MONGO_URI" -mongo-database video_agent -mongo-collection workflow_state
```

`configs/videoagent/` 中提供可解析的 `remote.example.json`、`model.example.json` 和 `prompt.example.json`。`remote` 描述图片、TTS、预览和成片能力；`model` 只配置右侧自然语言 Agent 的 ChatModel；`prompt` 配置工作流 Prompt 和变量映射。竞品图直接调用 Model Gateway，人物参考图直接调用 ModelHub/Gemini 并上传 ImageX，TTS 直接调用 Matx，预览直接调用 Seedance，成片直接调用 Meta；仓库不调用 Mega/Gen 工作流接口。图片、音频和视频分别使用 `image_media_url`、`audio_media_url`、`video_media_url` 解析存储 URI，避免跨存储域错误复用 URL 配置。生产数据库通过 `NewMongoStore(*mongo.Client, database, collection)` 注入，状态文档带 revision 并做乐观并发校验。回调入口验签后只调用 `NATSMessageBus.Publish`，不在 HTTP 请求线程恢复 DAG；`ConsumeCallbacks` 从 durable consumer 读取消息并调用 Runner。

原生 Fornax 模型配置示例：

```json
{
  "provider": "fornax",
  "model": "agent_supervisor_seed1.8_mvp1.7_sft",
  "api_key": "maas-api-key",
  "base_url": "https://ark-cn-beijing.bytedance.net/api/v3",
  "fornax": {
    "app_id": 0,
    "ak": "fornax-ak",
    "sk": "fornax-sk",
    "region": "cn-beijing"
  }
}
```

工作流 Prompt 配置示例；`bindings` 的 key 是 Fornax Prompt 变量名，value 是代码提供的稳定数据名。部署前必须按当前 Prompt/TCC 的真实变量要求调整，不允许把未确认变量硬编码进程序：

```json
{
  "fornax": {
    "app_id": 0,
    "ak": "fornax-ak",
    "sk": "fornax-sk",
    "region": "cn-beijing"
  },
  "planner": {
    "requirement": {
      "key": "aic.aic_tool.user_req_analysis",
      "bindings": {
        "query": "brief",
        "prod_name": "product_name",
        "product_img": "product_images"
      }
    },
    "clipscript": {
      "key": "jichuang.creative.dr_script_e2e",
      "bindings": {
        "query": "brief",
        "prod_name": "product_name",
        "product_img": "product_images",
        "requirement": "requirement",
        "creative_brief": "requirement_markdown"
      }
    },
    "competition": {
      "key": "aic.jichuang_agent.infringing_items_detection_new_schema",
      "model": "ad.genai.seedream_4_5",
      "width": 1440,
      "height": 2560
    },
    "tts": {
      "key": "aic.aic_agent.aigc_prompttts_raw2"
    },
    "character": {
      "key": "aic.aic_agent.aigc_planning",
      "model": "gemini-3-pro-image-preview",
      "width": 1024,
      "height": 1536
    }
  }
}
```

```json
{
  "base_url": "https://agent-capability.example.com",
  "api_key": "capability-api-key",
  "callback_secret": "callback-hmac-secret",
  "endpoints": {
    "image_audit": "/v1/image/audit",
    "prompt_shield": "/v1/prompt/shield"
  },
  "image_gateway": {
    "model": "ad.genai.seedream_4_5",
    "task_queue": "jichuang_agent",
    "width": 1440,
    "height": 2560
  },
  "character_image": {
    "gen_url": "https://modelhub.example.com/generate?key={{api_key}}",
    "edit_url": "https://modelhub.example.com/edit?key={{api_key}}",
    "api_keys": ["modelhub-api-key"],
    "model": "gemini-3-pro-image-preview",
    "width": 1024,
    "height": 1536,
    "imagex": {
      "access_key": "imagex-ak",
      "secret_key": "imagex-sk",
      "service_id": "r7j0lgfnz6"
    }
  },
  "prompt_tts": {
    "cpm": 280,
    "example_text": "这是一段用于试听音色效果的示例语音。",
    "speech_rate": 1.1
  },
  "seedance": {
    "base_url": "https://ark-cn-beijing.bytedance.net/api/v3/contents/generations/tasks",
    "api_key": "seedance-api-key",
    "model": "seedance-model-endpoint",
    "ratio": "9:16",
    "resolution": "720p",
    "duration": 5
  },
  "video_storage": {
    "space": "jichuang",
    "top_account_id": "top-account-id"
  },
  "finalvideo": {
    "width": 720,
    "height": 1280,
    "biz_id": 0
  }
}
```

`finalvideo.biz_id=0` 表示尚未配置，生产启动会直接失败。Mega 当前使用的 `BizId_Aic_Mega=118` 只用于核对原链路行为，新的 Agent 服务必须申请并填写自己的 Meta BizId，不能复用 Mega 身份。Fornax SDK 允许只配置 AK/SK；`app_id` 是可选身份字段，填写时必须与 AK/SK 属于同一应用。

NATS 默认连接 `nats://127.0.0.1:4222`，Stream 为 `VIDEO_AGENT_CALLBACKS`，Subject 为 `video_agent.callbacks`，DLQ Subject 为 `video_agent.callbacks.dead`，durable consumer 为 `video_agent_runner`。连接、Stream、主 Subject 和 consumer 可分别通过 `-nats-url`、`-nats-stream`、`-nats-subject` 和 `-nats-consumer` 覆盖。

审核 HTTP Adapter 不把 HTTP 2xx 等同于审核通过。`prompt_shield` 必须显式返回 `{"pass":true}` 或 `PASS/ALLOW`；`image_audit` 允许 `PASS/MARK/ALLOW`。`BLOCK/REJECT/DENY`、空响应和未知状态全部阻断后续生成。远端配置包含占位值，或启用审核 Endpoint 却未配置 `callback_secret` 时，应用在启动阶段直接失败。

远程模式同时要求 `-model-config` 和 `-prompt-config`：前者驱动画布 Agent 的 Eino ReAct，后者通过 Fornax 加载需求分析、分镜及资源规划 Prompt。只有本地开发模式允许使用内置规则 Agent 和确定性本地 Client。

生产构建固定使用仓库目标，避免遗漏原生 Client 的 build tags：

```bash
make videoagent-build
```

Fornax 当前依赖的 Kitex 生成代码使用 Thrift 0.13 接口，因此 `go.mod` 固定该兼容版本。NATS 使用官方 Go Client，不需要额外 build tag。

```text
GET  /projects/{project_id}
PUT  /projects/{project_id}/workflow
POST /agent/chat
GET  /conversations/{conversation_id}/messages
POST /projects/{project_id}/operations
POST /operations/{operation_id}/confirm
POST /operations/{operation_id}/reject
POST /runs                         # 返回 202 + pending Operation
GET  /runs/{run_id}
POST /runs/{run_id}/retry
POST /runs/{run_id}/cancel
POST /callbacks/{provider}
```

## 9. 验收、发布与回滚

验收必须覆盖：

1. 创建 Run 后存在 `requirement`、`clipscript`、`preview_video`、`finalvideo` Artifact。
2. `preview_video.ParentIDs` 包含 `clipscript` 和成功资源；`finalvideo.ParentIDs` 同时包含完整 `clipscript` 和所选预览。
3. TTS 每个角色先生成音色参考，再生成试听例句和真实分镜口播；`preview_audio_uri/url` 指向试听例句，`audio_uri/url` 指向真实口播。
4. `finalvideo` 通过 `VideoClient` 提交和查询正式成片；重启后只有 `submit_key` 也必须由该 Client 找回任务。
5. 重复 callback 不重复创建 Artifact；重启后 pending Job 可恢复且 `submit_key` 不重复提交。
6. 单个资源失败不会阻塞预览；预览或 `finalvideo` 失败在对应节点可见。
7. 取消中的 Run 不再启动后继节点，但仍消费 callback 和轮询结果直到远端 Job 收敛；完成取消后未完成节点进入 `canceled`，且 Run 不允许 retry。
8. Canvas 修改产生新 WorkflowVersion，旧 Run 仍按快照执行；Agent 不可绕过画布确认直接改写已运行版本。

```bash
GOTOOLCHAIN=go1.25.0 go test -race ./videoagent
GOTOOLCHAIN=go1.25.0 go vet ./videoagent
make videoagent-build
```

本地回滚时先停止 `cmd/videoagent`，保留 MongoDB 和 JetStream 数据，再切回上一二进制。远端发布必须以 Client 配置区分 Local 与真实能力；关闭真实 Client 后，不再提交新 Job，已持久化的 Run 仍可以由轮询或回调收敛。

项目固定使用 Go 1.25：普通和生产标签命令都应显式设置 `GOTOOLCHAIN=go1.25.0`。当前 `dynamicgo` 版本与 Go 1.26 的 `encoding/json` 内部符号不兼容，不能使用自动选择到的 Go 1.26 执行生产标签构建。

当前验收证据：默认构建、race 和 `fornax bytedance` 生产标签构建均已通过；本地 Canvas 已通过 Agent Operation 确认，从需求分析运行到 `finalvideo`，并在节点和产物区展示文本、图片、音频、视频占位结果及完整 Artifact 来源。本地 MongoDB 已验证整链路、跨实例租约和并发 WorkflowVersion 更新；JetStream 已验证失败重投、发布去重、客户端重启恢复、轮询恢复、死信，以及 Application 通过 MQ 完成整条工作流。Runner 会记录节点耗时、未知提交、对账失败、恢复失败、取消失败和租约续期失败。仓库不包含 Mega/Gen 工作流 SDK 依赖。上线前仍必须使用真实凭证验证 Fornax Prompt、Model Gateway、ModelHub/ImageX、Matx、Seedance、Meta 和生产 NATS 的 PPE E2E。

2026-08-05 使用现有 Supervisor MaaS 配置发起了真实自然语言 Agent 请求，请求已经到达模型网关，但 Endpoint 返回 `400`（关闭或暂时不可用，request id `0217859049703708a2cc6176b4dc89676d1081d755bfbd1d6f668`）。因此真实 ReAct 和完整远端 E2E 仍未通过，不能用本地确定性 Client 的成功结果替代该验收。

真实能力验收使用同一条 Agent、Operation、Mongo、JetStream 和 DAG 路径，不允许直接调用各 Client 绕过编排。配置以下环境变量后执行 `make videoagent-remote-e2e`：

```text
VIDEO_AGENT_E2E_REMOTE_CONFIG=/absolute/path/remote.json
VIDEO_AGENT_E2E_MODEL_CONFIG=/absolute/path/model.json
VIDEO_AGENT_E2E_PROMPT_CONFIG=/absolute/path/prompt.json
VIDEO_AGENT_E2E_MONGO_URI=mongodb://...
VIDEO_AGENT_E2E_NATS_URL=nats://...
VIDEO_AGENT_E2E_PRODUCT_NAME=软底厚底休闲鞋                  # 可选
VIDEO_AGENT_E2E_PRODUCT_IMAGES=https://.../product.png      # 可选，多条用换行或逗号分隔
VIDEO_AGENT_E2E_BRIEF=请结合商品卖点生成一条短视频广告       # 可选
VIDEO_AGENT_E2E_TIMEOUT=45m                                 # 可选
```

该验收会要求真实 ReAct Agent 提交 `run` Operation，确认后等待全部节点成功，并检查需求分析、分镜、竞品图、TTS、人物参考图、预览和正式成片 Artifact。所有媒体结果必须提供前端可展示的 HTTP(S) URL；`finalvideo` 必须同时关联 `clipscript` 和 `preview_video` 来源。
