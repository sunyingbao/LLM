package contextmanager

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

func TestSimpleContextManagerRequestResponseAndState(t *testing.T) {
	ctx := context.Background()
	middleware := New()
	manager, ok := middleware.(*SimpleContextManager)
	require.True(t, ok)
	require.Equal(t, "SimpleContextManager", manager.Name())
	require.Same(t, manager, manager.BuildStateHandler())

	initial := []*schema.Message{schema.SystemMessage("system")}
	request := []*schema.Message{schema.UserMessage("user")}
	modelRequest, err := manager.ModifyModelRequest(ctx, initial, request, nil)
	require.NoError(t, err)
	require.Equal(t, []*schema.Message{initial[0], request[0]}, modelRequest)

	response := schema.AssistantMessage("assistant", nil)
	modelResponse, err := manager.ModifyModelResponse(ctx, response, nil)
	require.NoError(t, err)
	require.Same(t, response, modelResponse)
	modelResponse, err = manager.ModifyModelResponse(ctx, nil, nil)
	require.NoError(t, err)
	require.Nil(t, modelResponse)

	state := manager.MarshalRuntimeState()
	restored := &SimpleContextManager{}
	require.NoError(t, restored.UnmarshalRuntimeState(state))
	require.Equal(t, manager.history, restored.history)
	require.Error(t, restored.UnmarshalRuntimeState("not-json"))
}

func TestSimpleContextManagerStreamResponse(t *testing.T) {
	ctx := context.Background()
	manager := &SimpleContextManager{}
	reader, writer := schema.Pipe[*schema.Message](2)
	writer.Send(schema.AssistantMessage("first", nil), nil)
	writer.Send(schema.AssistantMessage(" second", nil), nil)
	writer.Close()

	output, err := manager.ModifyModelStreamResponse(ctx, reader, nil)
	require.NoError(t, err)
	for {
		_, receiveErr := output.Recv()
		if errors.Is(receiveErr, io.EOF) {
			break
		}
		require.NoError(t, receiveErr)
	}
	require.Len(t, manager.history, 1)
	require.Equal(t, "first second", manager.history[0].Content)

	output, err = manager.ModifyModelStreamResponse(ctx, nil, nil)
	require.NoError(t, err)
	require.Nil(t, output)
}

func TestSimpleContextManagerStreamError(t *testing.T) {
	manager := &SimpleContextManager{}
	reader, writer := schema.Pipe[*schema.Message](1)
	wantErr := errors.New("stream failed")
	writer.Send(nil, wantErr)
	writer.Close()

	output, err := manager.ModifyModelStreamResponse(context.Background(), reader, nil)
	require.NoError(t, err)
	_, err = output.Recv()
	require.ErrorIs(t, err, wantErr)
	require.Empty(t, manager.history)

	emptyReader, emptyWriter := schema.Pipe[*schema.Message](1)
	emptyWriter.Close()
	output, err = manager.ModifyModelStreamResponse(context.Background(), emptyReader, nil)
	require.NoError(t, err)
	_, err = output.Recv()
	require.ErrorIs(t, err, io.EOF)
	require.Empty(t, manager.history)
}
