package videoagent

import (
	"go.mongodb.org/mongo-driver/v2/mongo"

	"eino-cli/videoagent/backend/persistence"
)

type Store = persistence.Store
type StoreData = persistence.StoreData
type StateBackend = persistence.StateBackend

var ErrProjectNotFound = persistence.ErrProjectNotFound

const nodeClaimTTL = persistence.NodeClaimTTL

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

func NewStore(path string) *Store {
	return persistence.NewStore(path)
}

func NewMongoStore(client *mongo.Client, database, collection string) (*Store, error) {
	return persistence.NewMongoStore(client, database, collection)
}
