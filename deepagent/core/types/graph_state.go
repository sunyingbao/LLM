package types

import (
	"context"
	"fmt"

	"code.byted.org/gopkg/logs/v2"
	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// GraphState 是 eino Graph 的执行状态，在单次 Run/Stream 内累积消息。
// 作为 ModifyModelRequest 的参数传入，中间件可按需读取或修改。
type GraphState struct {
	stateHolder map[string]graphStateEntry
	store       compose.CheckPointStore
}

type graphStateEntry struct {
	stateful RunTimeStateful
	persist  bool
}

func StateFromContext(ctx context.Context) *GraphState {
	i := ctx.Value("deep_agent_graph_state")
	if i == nil {
		return nil
	}

	res, ok := i.(*GraphState)
	if !ok {
		return nil
	}

	return res
}

func NewStateContext(ctx context.Context, state *GraphState) context.Context {
	return context.WithValue(ctx, "deep_agent_graph_state", state)
}

func NewGraphState(store compose.CheckPointStore) *GraphState {
	return &GraphState{
		stateHolder: map[string]graphStateEntry{},
		store:       store,
	}
}

type RunTimeStateful interface {
	MarshalRuntimeState() string
	UnmarshalRuntimeState(data string) error
}

func (as *GraphState) GetStateful(name string) RunTimeStateful {
	entry, ok := as.stateHolder[name]
	if !ok {
		return nil
	}
	return entry.stateful
}

func (as *GraphState) RegisterStateful(name string, stateful RunTimeStateful) {
	as.RegisterStatefulWithPersistence(name, stateful, true)
}

func (as *GraphState) RegisterRuntimeOnlyStateful(name string, stateful RunTimeStateful) {
	as.RegisterStatefulWithPersistence(name, stateful, false)
}

func (as *GraphState) RegisterStatefulWithPersistence(name string, stateful RunTimeStateful, persist bool) {
	as.stateHolder[name] = graphStateEntry{
		stateful: stateful,
		persist:  persist,
	}
}

func (as *GraphState) Save(ctx context.Context, graphCheckpointID string) error {
	if as.store == nil {
		return nil
	}
	jsonBytes, err := as.encode()
	if err != nil {
		return err
	}
	key := graphStateKey(graphCheckpointID)
	err = as.store.Set(ctx, key, jsonBytes)
	if err != nil {
		logs.CtxError(ctx, "[GraphState::Save] failed, err: %v key:%s", err, key)
		return err
	}
	logs.CtxInfo(ctx, "[GraphState::Save] success, key:%s", key)
	return nil
}

func (as *GraphState) Resume(ctx context.Context, graphCheckpointID string) error {
	if as.store == nil {
		return nil
	}

	key := graphStateKey(graphCheckpointID)
	data, exist, err := as.store.Get(ctx, key)
	if !exist {
		logs.CtxInfo(ctx, "[GraphState::Resume] not exist, key:%s", key)
		return nil
	}

	if err != nil {
		logs.CtxError(ctx, "[GraphState::Resume] failed, err: %v key:%s", err, key)
		return err
	}
	return as.decode(data)
}

func graphStateKey(graphCheckpointID string) string {
	return fmt.Sprintf("%s:%s", "deepagent_graph_state_", graphCheckpointID)
}

func (as *GraphState) encode() ([]byte, error) {
	logs.Info("[AgentState::encode] called")
	jsonData := map[string]string{}
	for name, entry := range as.stateHolder {
		if !entry.persist {
			continue
		}
		data := entry.stateful.MarshalRuntimeState()
		jsonData[name] = data
	}

	return sonic.Marshal(jsonData)
}

func (as *GraphState) decode(data []byte) error {
	logs.Info("[AgentState::decode] called")
	jsonData := map[string]string{}
	var err error
	if err = sonic.Unmarshal(data, &jsonData); err != nil {
		return err
	}
	for name, entry := range as.stateHolder {
		if !entry.persist {
			continue
		}
		if id, ok := jsonData[name]; ok {
			if err := entry.stateful.UnmarshalRuntimeState(id); err != nil {
				return err
			}
		}
	}

	return nil
}

// GraphLocalState 是 Eino StateHandler 所需的 Graph LocalState 占位类型。
type GraphLocalState struct{}

func init() {
	schema.RegisterName[*GraphLocalState]("my_empty_state")
}
