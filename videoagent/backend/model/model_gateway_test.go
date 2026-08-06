package model

import "context"

type fakeModelGateway struct {
	request ModelTaskRequest
	jobID   string
	status  ModelTaskStatus
}

func (gateway *fakeModelGateway) CreateTask(_ context.Context, request ModelTaskRequest) (string, error) {
	gateway.request = request
	return gateway.jobID, nil
}

func (gateway *fakeModelGateway) GetTask(context.Context, string) (ModelTaskStatus, error) {
	return gateway.status, nil
}
