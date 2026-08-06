//go:build fornax

package videoagent

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	fornaxchat "code.byted.org/flowdevops/fornax_sdk/domain/chatmodel"
	"code.byted.org/flowdevops/fornax_sdk/domain/prompt"
	"github.com/cloudwego/eino/schema"
)

func TestFornaxPromptVariablesPreserveObjectsAndArrays(t *testing.T) {
	object := map[string]any{"semantic_clips": []any{map[string]any{"id": 1}}}
	array := []string{"one", "two"}
	variables, err := fornaxPromptVariables(PromptRequest{Variables: map[string]any{"script": object, "items": array}})
	if err != nil {
		t.Fatal(err)
	}
	byKey := make(map[string]*prompt.Variable, len(variables))
	for _, variable := range variables {
		byKey[variable.Key] = variable
	}
	if byKey["script"].VariableType != prompt.VariableTypeObject || !reflect.DeepEqual(byKey["script"].Value, object) {
		t.Fatalf("script variable = %#v", byKey["script"])
	}
	if byKey["items"].VariableType != prompt.VariableTypeArray || !reflect.DeepEqual(byKey["items"].Value, array) {
		t.Fatalf("items variable = %#v", byKey["items"])
	}
}

func TestFornaxResponseStreamReadsUnderlyingEOF(t *testing.T) {
	reader := &fakeFornaxReader{responses: []*fornaxchat.ChatCompletionStreamResponse{{
		Choices: []*fornaxchat.ChatCompletionStreamChoice{{
			Delta:        fornaxchat.ChatCompletionStreamChoiceDelta{Content: "done"},
			FinishReason: fornaxchat.FinishReasonStop,
		}},
	}}}
	stream := &fornaxResponseStream{reader: reader, calls: make(map[string]*schema.ToolCall)}

	message, err := stream.Recv(context.Background())
	if err != nil || message.Content != "done" {
		t.Fatalf("first Recv() = %#v, %v", message, err)
	}
	message, err = stream.Recv(context.Background())
	if err != nil || message.ResponseMeta == nil || message.ResponseMeta.FinishReason != "stop" {
		t.Fatalf("finish Recv() = %#v, %v", message, err)
	}
	if _, err = stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("final Recv() error = %v, want EOF", err)
	}
	if reader.recvCount != 2 {
		t.Fatalf("underlying Recv() count = %d, want frame plus EOF", reader.recvCount)
	}
}

func TestFornaxResponseStreamFlushesToolCallAtEOF(t *testing.T) {
	reader := &fakeFornaxReader{responses: []*fornaxchat.ChatCompletionStreamResponse{{
		Choices: []*fornaxchat.ChatCompletionStreamChoice{{
			Delta: fornaxchat.ChatCompletionStreamChoiceDelta{ToolCalls: []*fornaxchat.ToolCall{{
				ID: "call-1",
				FunctionCall: &fornaxchat.FunctionCall{
					Name:      "create_video",
					Arguments: `{"prompt":"shoe"}`,
				},
			}}},
		}},
	}}}
	stream := &fornaxResponseStream{reader: reader, calls: make(map[string]*schema.ToolCall)}

	message, err := stream.Recv(context.Background())
	if err != nil || len(message.ToolCalls) != 1 {
		t.Fatalf("Recv() = %#v, %v", message, err)
	}
	if message.ToolCalls[0].Function.Name != "create_video" || message.ToolCalls[0].Function.Arguments != `{"prompt":"shoe"}` {
		t.Fatalf("tool call = %#v", message.ToolCalls[0])
	}
	if _, err = stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("second Recv() error = %v, want EOF", err)
	}
}

type fakeFornaxReader struct {
	responses []*fornaxchat.ChatCompletionStreamResponse
	recvCount int
}

func (reader *fakeFornaxReader) Recv(context.Context) (*fornaxchat.ChatCompletionStreamResponse, error) {
	reader.recvCount++
	if len(reader.responses) == 0 {
		return nil, io.EOF
	}
	response := reader.responses[0]
	reader.responses = reader.responses[1:]
	return response, nil
}

func (*fakeFornaxReader) Close() error { return nil }
