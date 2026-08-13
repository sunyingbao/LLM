package contextmanager

import (
	"context"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/core/graph"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/types"
	utils2 "eino-cli/deepagent/core/utils"
	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
)

type SimpleContextManager struct {
	middleware.BaseMiddleware
	history []*schema.Message
}

func New() middleware.Middleware {
	return &SimpleContextManager{}
}

func (c *SimpleContextManager) Name() string {
	return "SimpleContextManager"
}

func (c *SimpleContextManager) ModifyModelRequest(ctx context.Context, initialContext []*schema.Message, messages []*schema.Message, _ *types.GraphState) ([]*schema.Message, error) {
	// append user input or tool rsp to history
	c.history = append(c.history, messages...)

	var modelRequest []*schema.Message
	// append initial context to model request
	modelRequest = append(modelRequest, initialContext...)
	// append history to model request
	modelRequest = append(modelRequest, c.history...)

	return modelRequest, nil
}

func (c *SimpleContextManager) ModifyModelResponse(ctx context.Context, response *schema.Message, _ *types.GraphState) (*schema.Message, error) {
	if response == nil {
		return nil, nil
	}

	c.history = append(c.history, response)
	return response, nil
}

func (c *SimpleContextManager) ModifyModelStreamResponse(ctx context.Context, modelResp *schema.StreamReader[*schema.Message], state *types.GraphState) (*schema.StreamReader[*schema.Message], error) {
	if modelResp == nil {
		return modelResp, nil
	}
	outputReader, outputWriter := schema.Pipe[*schema.Message](1000)

	go func() {
		defer func() {
			utils2.PanicGuard(ctx)
			modelResp.Close()
			outputWriter.Close()
		}()

		merger := graph.NewStreamMessageMerger(func(ctx context.Context, chunk *schema.Message) {
			outputWriter.Send(chunk, nil)
		})
		fullMessage, err := merger.Merge(ctx, modelResp)
		if err != nil {
			logs.Error("[SimpleContextManager] merge stream message failed, err:%v", err)
			outputWriter.Send(nil, err)
			return
		}
		if fullMessage == nil {
			return
		}
		c.history = append(c.history, fullMessage)
	}()

	return outputReader, nil
}

func (c *SimpleContextManager) BuildStateHandler() types.RunTimeStateful {
	return c
}

func (c *SimpleContextManager) MarshalRuntimeState() string {
	data, _ := sonic.MarshalString(c.history)
	return data
}

func (c *SimpleContextManager) UnmarshalRuntimeState(data string) error {
	err := sonic.UnmarshalString(data, &c.history)
	logs.Info("[SimpleContextManager] resume data:%s", utils2.ToString(c.history))
	return err
}
