//go:build !windows

package worker_test

import (
	"context"
	"reflect"
	"testing"

	worker "eino-cli/deepagent/cloud/worker"
)

type approvalStore struct{}

func (approvalStore) IsAllowed(context.Context, *worker.ThreadInfo, string, string) bool {
	return false
}

func (approvalStore) Allow(context.Context, *worker.ThreadInfo, string, string) {}

var _ worker.ApprovalStore = approvalStore{}
var _ worker.ThreadProfileResolver = func(context.Context, worker.ThreadProfileRequest) (worker.ResolvedThreadProfile, error) {
	return worker.ResolvedThreadProfile{}, nil
}
var _ worker.TurnProfileResolver = func(context.Context, worker.TurnProfileRequest) (worker.ResolvedTurnProfile, error) {
	return worker.ResolvedTurnProfile{}, nil
}

func TestCoordinatorImplementationsDoNotExposeGeneratedFields(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(worker.Worker{}),
		reflect.TypeOf(worker.CoordinatorClient{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			if typ.Field(i).IsExported() {
				t.Fatalf("%s exposes implementation field %s", typ.Name(), typ.Field(i).Name)
			}
		}
	}
}
