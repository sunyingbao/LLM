package application

import (
	"context"

	"eino-cli/videoagent/backend/persistence"
)

type StoreData = persistence.StoreData

func emptyStoreData() StoreData {
	return persistence.EmptyStoreData()
}

func normalizeStoreData(data StoreData) StoreData {
	return persistence.NormalizeStoreData(data)
}

func inputArtifacts(run Run, node NodeRun) []Artifact {
	return persistence.InputArtifacts(run, node)
}

func findNodeRun(run Run, target NodeRun) int {
	return persistence.FindNodeRun(run, target)
}

func useDirectCallbackPublisher(application *Application) {
	application.SetMessageQueue(MessagePublisherFunc(func(ctx context.Context, message CallbackMessage) error {
		return application.Runner.ProcessCallback(ctx, message)
	}), nil)
}
