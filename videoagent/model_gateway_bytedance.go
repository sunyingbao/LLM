//go:build bytedance

package videoagent

import (
	"context"
	"fmt"

	"code.byted.org/overpass/lab_creative_model_gateway/kitex_gen/lab/creative_model"
	"code.byted.org/overpass/lab_creative_model_gateway/rpc/lab_creative_model_gateway"
)

type bytedanceModelGateway struct{}

func NewBytedanceModelGateway() (ModelGateway, error) {
	return bytedanceModelGateway{}, nil
}

func (bytedanceModelGateway) Generate(ctx context.Context, request ModelTaskRequest) ([]byte, error) {
	response, err := lab_creative_model_gateway.RawCall.Generate(ctx, &creative_model.GenerateReq{
		Input: request.Input, ModelKey: request.Model,
	})
	if err != nil {
		return nil, err
	}
	if response == nil || len(response.GetOutput()) == 0 {
		return nil, fmt.Errorf("model gateway returned empty output")
	}
	return response.GetOutput(), nil
}

func (bytedanceModelGateway) CreateTask(ctx context.Context, request ModelTaskRequest) (string, error) {
	response, err := lab_creative_model_gateway.RawCall.CreateTask(ctx, &creative_model.CreateTaskReq{
		Input: request.Input, ModelKey: request.Model, TaskQueue: request.TaskQueue, Extra: request.Extra,
	})
	if err != nil {
		return "", err
	}
	if response == nil || response.GetTaskId() == "" {
		return "", fmt.Errorf("model gateway returned an empty task id")
	}
	return response.GetTaskId(), nil
}

func (bytedanceModelGateway) GetTask(ctx context.Context, taskID string) (ModelTaskStatus, error) {
	response, err := lab_creative_model_gateway.RawCall.GetTaskResult_(ctx, &creative_model.GetTaskResultReq{TaskId: taskID})
	if err != nil {
		return ModelTaskStatus{}, err
	}
	if response == nil {
		return ModelTaskStatus{}, fmt.Errorf("model gateway returned an empty task status")
	}
	return ModelTaskStatus{
		Code: response.GetCode(), Status: response.GetStatus(), Result: response.GetResult_(),
		BizCode: response.GetBizCode(), BizMessage: response.GetBizMessage(),
	}, nil
}

var _ ModelGateway = bytedanceModelGateway{}
