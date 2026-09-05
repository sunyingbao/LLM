package cloud

import (
	"context"

	"eino-cli/deepagent/coordinator"
)

type taskToolClientMock struct {
	CoordinatorClient
	createThread func(context.Context, coordinator.CreateThreadRequest) (coordinator.CreateThreadResult, error)
	sendMessage  func(context.Context, coordinator.SubmitInputRequest) (coordinator.SubmitInputResult, error)
	closeThread  func(context.Context, coordinator.RequestThreadCloseRequest) (*coordinator.RequestThreadCloseResult, error)
	listEvents   func(context.Context, coordinator.ListEventsRequest) (coordinator.ListEventsResult, error)
}

func (m *taskToolClientMock) CreateThread(ctx context.Context, request coordinator.CreateThreadRequest) (result coordinator.CreateThreadResult, err error) {
	return m.createThread(ctx, request)
}

func (m *taskToolClientMock) SubmitInput(ctx context.Context, request coordinator.SubmitInputRequest) (result coordinator.SubmitInputResult, err error) {
	return m.sendMessage(ctx, request)
}

func (m *taskToolClientMock) RequestThreadClose(ctx context.Context, request coordinator.RequestThreadCloseRequest) (result *coordinator.RequestThreadCloseResult, err error) {
	return m.closeThread(ctx, request)
}

func (m *taskToolClientMock) ListEvents(ctx context.Context, request coordinator.ListEventsRequest) (result coordinator.ListEventsResult, err error) {
	return m.listEvents(ctx, request)
}

type taskToolMockSetter struct {
	client *taskToolClientMock
}

func (s taskToolMockSetter) CreateThread(mock func(context.Context, coordinator.CreateThreadRequest) (coordinator.CreateThreadResult, error)) {
	s.client.createThread = mock
}

func (s taskToolMockSetter) SubmitInput(mock func(context.Context, coordinator.SubmitInputRequest) (coordinator.SubmitInputResult, error)) {
	s.client.sendMessage = mock
}

func (s taskToolMockSetter) RequestThreadClose(mock func(context.Context, coordinator.RequestThreadCloseRequest) (*coordinator.RequestThreadCloseResult, error)) {
	s.client.closeThread = mock
}

func (s taskToolMockSetter) ListEvents(mock func(context.Context, coordinator.ListEventsRequest) (coordinator.ListEventsResult, error)) {
	s.client.listEvents = mock
}

var taskToolMocks = &taskToolClientMock{}

var coordinatorrpc = struct {
	SetMock taskToolMockSetter
}{SetMock: taskToolMockSetter{client: taskToolMocks}}

func init() {
	fallbackCoordinatorClient = taskToolMocks
}
