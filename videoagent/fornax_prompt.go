//go:build fornax

package videoagent

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"code.byted.org/flowdevops/fornax_sdk"
	"code.byted.org/flowdevops/fornax_sdk/domain"
	"code.byted.org/flowdevops/fornax_sdk/domain/prompt"
	"code.byted.org/flowdevops/fornax_sdk/domain/prompt/execution"
)

type fornaxPromptExecutor struct {
	client *fornax_sdk.Client
}

func NewFornaxPromptExecutor(config FornaxConfig) (PromptExecutor, error) {
	if err := validateFornaxConfig(&config); err != nil {
		return nil, err
	}
	client, err := fornax_sdk.NewClient(fornaxClientConfig(&config))
	if err != nil {
		return nil, err
	}
	return &fornaxPromptExecutor{client: client}, nil
}

func fornaxClientConfig(config *FornaxConfig) *domain.Config {
	result := &domain.Config{Identity: &domain.Identity{}}
	if config == nil {
		return result
	}
	result.Identity.APPID = config.AppID
	result.Identity.AK = config.AK
	result.Identity.SK = config.SK
	if config.Region != "" {
		region := config.Region
		result.FornaxCustomRegion = &region
	}
	return result
}

func (executor *fornaxPromptExecutor) Execute(ctx context.Context, request PromptRequest) (string, error) {
	if executor == nil || executor.client == nil {
		return "", fmt.Errorf("Fornax prompt executor is nil")
	}
	if strings.TrimSpace(request.Key) == "" {
		return "", fmt.Errorf("prompt key is empty")
	}
	if request.Text == "" && len(request.ImageVariables) == 0 {
		result, err := executor.client.ExecutePromptLocal(ctx, &execution.ExecutePromptLocalParam{
			PromptKey: request.Key,
			Variables: request.Variables,
		})
		if err != nil {
			return "", err
		}
		if result == nil || len(result.Choices) == 0 || result.Choices[0] == nil {
			return "", fmt.Errorf("prompt %s returned no choice", request.Key)
		}
		return requirePromptContent(request.Key, result.Choices[0].Message.Content)
	}

	variables, err := fornaxPromptVariables(request)
	if err != nil {
		return "", err
	}
	param := &prompt.ExecutePromptParam{PromptKey: request.Key, Variables: variables}
	if request.Text != "" {
		param.Contexts = []*prompt.Message{{
			MessageType: prompt.MessageTypeUser,
			Parts:       []*prompt.ContentPart{{Type: prompt.ContentTypeText, Text: &request.Text}},
		}}
	}
	result, err := executor.client.ExecutePrompt(ctx, param)
	if err != nil {
		return "", err
	}
	if result == nil || result.Item == nil {
		return "", fmt.Errorf("prompt %s returned no result", request.Key)
	}
	return requirePromptContent(request.Key, result.Item.Content)
}

func fornaxPromptVariables(request PromptRequest) ([]*prompt.Variable, error) {
	variables := make([]*prompt.Variable, 0, len(request.Variables)+len(request.ImageVariables))
	for key, value := range request.Variables {
		variableType := prompt.VariableTypeObject
		switch value.(type) {
		case string:
			variableType = prompt.VariableTypeString
		case bool:
			variableType = prompt.VariableTypeBoolean
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			variableType = prompt.VariableTypeInteger
		case float32, float64:
			variableType = prompt.VariableTypeNumber
		}
		if value != nil {
			kind := reflect.TypeOf(value).Kind()
			if kind == reflect.Array || kind == reflect.Slice {
				variableType = prompt.VariableTypeArray
			}
		}
		variables = append(variables, &prompt.Variable{Key: key, VariableType: variableType, Value: value})
	}
	for key, urls := range request.ImageVariables {
		parts := make([]*prompt.ContentPart, 0, len(urls))
		for _, imageURL := range urls {
			if strings.TrimSpace(imageURL) != "" {
				parts = append(parts, &prompt.ContentPart{Type: prompt.ContentTypeImage, Image: &prompt.Image{URL: imageURL}})
			}
		}
		variables = append(variables, &prompt.Variable{Key: key, VariableType: prompt.VariableTypePlaceholder, Value: parts})
	}
	return variables, nil
}

func requirePromptContent(key, content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("prompt %s returned empty content", key)
	}
	return content, nil
}

var _ PromptExecutor = (*fornaxPromptExecutor)(nil)
