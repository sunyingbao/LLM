package graph

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/mohae/deepcopy"
)

func CopyMessage(msg *schema.Message) *schema.Message {
	if msg == nil {
		return nil
	}
	// pay attention, this may issue panic
	return deepcopy.Copy(msg).(*schema.Message)
}

func StreamHasToolCall(ctx context.Context, modelResp *schema.StreamReader[*schema.Message]) (bool, error) {
	for {
		chunk, err := modelResp.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, err
		}
		if len(chunk.ToolCalls) != 0 {
			return true, nil
		}
	}
}

type StreamMessageMerger struct {
	firstChunk    *schema.Message
	toolCollector *ToolCallCollector
	onChunk       func(ctx context.Context, chunk *schema.Message)
	content       strings.Builder
	reasoning     strings.Builder
}

func NewStreamMessageMerger(onChunk func(ctx context.Context, chunk *schema.Message)) *StreamMessageMerger {
	return &StreamMessageMerger{
		toolCollector: NewToolCallCollector(),
		onChunk:       onChunk,
	}
}

func (s *StreamMessageMerger) Merge(ctx context.Context, modelResp *schema.StreamReader[*schema.Message]) (*schema.Message, error) {
	var completeCalls []schema.ToolCall

	for {
		chunk, err := modelResp.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}

		if s.firstChunk == nil {
			s.firstChunk = chunk
		}
		if s.onChunk != nil {
			s.onChunk(ctx, chunk)
		}
		completeCalls = append(completeCalls, s.toolCollector.Collect(ctx, chunk)...)
		s.collectContent(chunk)
	}

	newMsg := CopyMessage(s.firstChunk)
	if newMsg == nil {
		return nil, nil
	}

	newMsg.Content = s.content.String()
	newMsg.ReasoningContent = s.reasoning.String()
	newMsg.ToolCalls = append(completeCalls, s.toolCollector.GetRepairedToolCalls(ctx)...)
	return newMsg, nil
}

func (s *StreamMessageMerger) collectContent(chunk *schema.Message) {
	if chunk.Content != "" {
		s.content.WriteString(chunk.Content)
	}
	if chunk.ReasoningContent != "" {
		s.reasoning.WriteString(chunk.ReasoningContent)
	}
}
