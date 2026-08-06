//go:build fornax

package videoagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"code.byted.org/flowdevops/fornax_sdk"
	fornaxchat "code.byted.org/flowdevops/fornax_sdk/domain/chatmodel"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/getkin/kin-openapi/openapi3"
)

type nativeFornaxModel struct {
	model modelChat
	tools []*fornaxchat.Tool
}

type modelChat interface {
	Stream(context.Context, []*fornaxchat.ChatMessage, ...fornaxchat.Option) (fornaxchat.StreamReader, error)
}

func newFornaxChatModel(ctx context.Context, config ChatModelConfig) (model.ToolCallingChatModel, error) {
	if strings.TrimSpace(config.Model) == "" {
		return nil, fmt.Errorf("chat model name is empty")
	}
	client, err := fornax_sdk.NewClient(fornaxClientConfig(config.Fornax))
	if err != nil {
		return nil, fmt.Errorf("create Fornax client: %w", err)
	}

	modelConfig := &fornaxchat.Config{
		Provider: fornaxchat.MaaS,
		MassConfig: &fornaxchat.MaasConfig{
			Model:   config.Model,
			APIKey:  config.APIKey,
			BaseURL: config.BaseURL,
		},
	}
	if config.Fornax != nil {
		modelConfig.MassConfig.Region = config.Fornax.Region
	}
	chatModel, err := client.GetChatModel(ctx, modelConfig)
	if err != nil {
		return nil, fmt.Errorf("create Fornax chat model: %w", err)
	}
	return &nativeFornaxModel{model: chatModel}, nil
}

func (native *nativeFornaxModel) Generate(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.Message, error) {
	stream, err := native.Stream(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.ConcatMessageStream(stream)
}

func (native *nativeFornaxModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if native == nil || native.model == nil {
		return nil, fmt.Errorf("Fornax chat model is nil")
	}
	if len(input) == 0 {
		return nil, fmt.Errorf("chat messages are empty")
	}
	tools := native.tools
	common := model.GetCommonOptions(nil, options...)
	if len(common.Tools) > 0 {
		var err error
		tools, err = toFornaxTools(common.Tools)
		if err != nil {
			return nil, err
		}
	}
	reader, err := native.model.Stream(ctx, toFornaxMessages(input), fornaxchat.WithTools(tools))
	if err != nil {
		return nil, err
	}
	response := &fornaxResponseStream{reader: reader, calls: make(map[string]*schema.ToolCall)}
	return response.einoStream(ctx), nil
}

func (native *nativeFornaxModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	tools, err := toFornaxTools(infos)
	if err != nil {
		return nil, err
	}
	clone := *native
	clone.tools = tools
	return &clone, nil
}

func toFornaxTools(infos []*schema.ToolInfo) ([]*fornaxchat.Tool, error) {
	tools := make([]*fornaxchat.Tool, 0, len(infos))
	for _, info := range infos {
		if info == nil || info.Name == "" {
			continue
		}
		parameters, err := toOpenAPISchema(info)
		if err != nil {
			return nil, err
		}
		tools = append(tools, &fornaxchat.Tool{
			Type: fornaxchat.ToolTypeFunction,
			Function: &fornaxchat.FunctionDefinition{
				Name:        info.Name,
				Description: info.Desc,
				Parameters:  parameters,
			},
		})
	}
	return tools, nil
}

func toOpenAPISchema(info *schema.ToolInfo) (*openapi3.Schema, error) {
	if info.ParamsOneOf == nil {
		return nil, nil
	}
	jsonSchema, err := info.ToJSONSchema()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(jsonSchema)
	if err != nil {
		return nil, err
	}
	var openAPISchema openapi3.Schema
	if err = json.Unmarshal(data, &openAPISchema); err != nil {
		return nil, err
	}
	return &openAPISchema, nil
}

func toFornaxMessages(messages []*schema.Message) []*fornaxchat.ChatMessage {
	converted := make([]*fornaxchat.ChatMessage, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			continue
		}
		name := message.Name
		if message.Role == schema.Tool && name == "" {
			name = message.ToolName
		}
		item := &fornaxchat.ChatMessage{
			Role:             fornaxchat.RoleType(message.Role),
			Content:          message.Content,
			ReasoningContent: message.ReasoningContent,
			Name:             name,
			ToolCallID:       message.ToolCallID,
		}
		for _, part := range message.UserInputMultiContent {
			if convertedPart := toFornaxPart(part); convertedPart != nil {
				item.MultiContent = append(item.MultiContent, convertedPart)
			}
		}
		for _, call := range message.ToolCalls {
			item.ToolCalls = append(item.ToolCalls, &fornaxchat.ToolCall{
				ID:   call.ID,
				Type: fornaxchat.ToolType(call.Type),
				FunctionCall: &fornaxchat.FunctionCall{
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				},
			})
		}
		converted = append(converted, item)
	}
	return converted
}

func toFornaxPart(part schema.MessageInputPart) *fornaxchat.ChatMessagePart {
	result := &fornaxchat.ChatMessagePart{Text: part.Text}
	switch part.Type {
	case schema.ChatMessagePartTypeText:
		result.Type = fornaxchat.ChatMessagePartTypeText
	case schema.ChatMessagePartTypeImageURL:
		if part.Image == nil {
			return nil
		}
		result.Type = fornaxchat.ChatMessagePartTypeImageURL
		if part.Image.URL != nil {
			result.ImageURL = &fornaxchat.ChatMessageImageURL{URL: *part.Image.URL, Detail: fornaxchat.ImageURLDetail(part.Image.Detail)}
		} else if part.Image.Base64Data != nil {
			result.Type = fornaxchat.ChatMessagePartTypeImageBinary
			result.ImageBinary = &fornaxchat.ChatMessageImageBinary{Data: *part.Image.Base64Data, MimeType: part.Image.MIMEType}
		} else {
			return nil
		}
	case schema.ChatMessagePartTypeVideoURL:
		if part.Video == nil {
			return nil
		}
		result.Type = fornaxchat.ChatMessagePartTypeVideoURL
		if part.Video.URL != nil {
			result.VideoURL = &fornaxchat.ChatMessageVideoURL{URL: *part.Video.URL}
		} else if part.Video.Base64Data != nil {
			result.Type = fornaxchat.ChatMessagePartTypeVideoBinary
			result.VideoBinary = &fornaxchat.ChatMessageVideoBinary{Data: *part.Video.Base64Data, MimeType: part.Video.MIMEType}
		} else {
			return nil
		}
	case schema.ChatMessagePartTypeAudioURL:
		if part.Audio == nil {
			return nil
		}
		result.Type = fornaxchat.ChatMessagePartTypeAudioURL
		if part.Audio.URL != nil {
			result.AudioURL = &fornaxchat.ChatMessageAudioURL{URL: *part.Audio.URL}
		} else if part.Audio.Base64Data != nil {
			result.Type = fornaxchat.ChatMessagePartTypeAudioBinary
			result.AudioBinary = &fornaxchat.ChatMessageAudioBinary{Data: *part.Audio.Base64Data, MimeType: part.Audio.MIMEType}
		} else {
			return nil
		}
	default:
		return nil
	}
	return result
}

type fornaxResponseStream struct {
	reader      fornaxchat.StreamReader
	pending     []*schema.Message
	calls       map[string]*schema.ToolCall
	callOrder   []string
	latestUsage *schema.TokenUsage
	closed      bool
}

func (stream *fornaxResponseStream) einoStream(ctx context.Context) *schema.StreamReader[*schema.Message] {
	out, writer := schema.Pipe[*schema.Message](8)
	go func() {
		defer writer.Close()
		defer stream.reader.Close()
		for {
			message, err := stream.Recv(ctx)
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil || writer.Send(message, err) {
				return
			}
		}
	}()
	return out
}

func (stream *fornaxResponseStream) Recv(ctx context.Context) (*schema.Message, error) {
	if len(stream.pending) > 0 {
		message := stream.pending[0]
		stream.pending = stream.pending[1:]
		return message, nil
	}
	if stream.closed {
		return nil, io.EOF
	}
	for attempts := 0; attempts < 256; attempts++ {
		response, err := stream.reader.Recv(ctx)
		if errors.Is(err, io.EOF) {
			stream.closed = true
			calls := stream.flushCalls()
			if len(calls) > 0 {
				return stream.assistant(nil, calls, "tool_calls"), nil
			}
			return nil, io.EOF
		}
		if err != nil {
			stream.closed = true
			return nil, err
		}
		if response == nil {
			continue
		}
		stream.updateUsage(response)
		for _, choice := range response.Choices {
			if choice == nil {
				continue
			}
			if choice.Delta.Role == fornaxchat.RoleTypeTool || choice.Delta.ToolCallID != "" {
				return nil, fmt.Errorf("provider returned tool result in model stream")
			}
			stream.appendCalls(choice.Delta.ToolCalls)
			if choice.Delta.Content != "" || choice.Delta.ReasoningContent != "" {
				stream.pending = append(stream.pending, stream.assistantText(choice.Delta.Content, choice.Delta.ReasoningContent))
			}
			if choice.FinishReason == fornaxchat.FinishReasonToolCalls {
				stream.pending = append(stream.pending, stream.assistant(nil, stream.flushCalls(), "tool_calls"))
			} else if choice.FinishReason != "" {
				stream.pending = append(stream.pending, stream.assistant(nil, stream.flushCalls(), string(choice.FinishReason)))
			}
		}
		if len(stream.pending) > 0 {
			message := stream.pending[0]
			stream.pending = stream.pending[1:]
			return message, nil
		}
	}
	stream.closed = true
	return nil, fmt.Errorf("Fornax model stream exceeded frame limit")
}

func (stream *fornaxResponseStream) appendCalls(calls []*fornaxchat.ToolCall) {
	for _, call := range calls {
		if call == nil || call.FunctionCall == nil {
			continue
		}
		id := call.ID
		if id == "" && len(stream.callOrder) > 0 {
			id = stream.callOrder[len(stream.callOrder)-1]
		}
		if id == "" {
			id = fmt.Sprintf("call-%d", len(stream.callOrder)+1)
		}
		assembled := stream.calls[id]
		if assembled == nil {
			assembled = &schema.ToolCall{ID: id, Type: string(fornaxchat.ToolTypeFunction)}
			stream.calls[id] = assembled
			stream.callOrder = append(stream.callOrder, id)
		}
		if assembled.Function.Name == "" {
			assembled.Function.Name = call.FunctionCall.Name
		}
		assembled.Function.Arguments += call.FunctionCall.Arguments
	}
}

func (stream *fornaxResponseStream) flushCalls() []schema.ToolCall {
	calls := make([]schema.ToolCall, 0, len(stream.callOrder))
	for _, id := range stream.callOrder {
		if call := stream.calls[id]; call != nil {
			calls = append(calls, *call)
		}
	}
	stream.callOrder = nil
	stream.calls = make(map[string]*schema.ToolCall)
	return calls
}

func (stream *fornaxResponseStream) assistantText(content, reasoning string) *schema.Message {
	return stream.assistant(&schema.Message{Role: schema.Assistant, Content: content, ReasoningContent: reasoning}, nil, "")
}

func (stream *fornaxResponseStream) assistant(message *schema.Message, calls []schema.ToolCall, finish string) *schema.Message {
	if message == nil {
		message = &schema.Message{Role: schema.Assistant}
	}
	message.ToolCalls = calls
	if finish != "" || stream.latestUsage != nil {
		message.ResponseMeta = &schema.ResponseMeta{FinishReason: finish, Usage: stream.latestUsage}
	}
	return message
}

func (stream *fornaxResponseStream) updateUsage(response *fornaxchat.ChatCompletionStreamResponse) {
	usage := response.Usage
	if usage.TotalTokens == 0 && usage.PromptTokens == 0 && usage.CompletionTokens == 0 {
		return
	}
	stream.latestUsage = &schema.TokenUsage{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens}
}

var _ model.ToolCallingChatModel = (*nativeFornaxModel)(nil)
