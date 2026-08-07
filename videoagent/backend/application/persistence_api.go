package application

import (
	"go.mongodb.org/mongo-driver/v2/mongo"

	"eino-cli/videoagent/backend/persistence"
)

type Store = persistence.Store

const nodeClaimTTL = persistence.NodeClaimTTL

func NewStore(path string) *Store {
	return persistence.NewStore(path)
}

func NewMongoStore(client *mongo.Client, database, collection string) (*Store, error) {
	return persistence.NewMongoStore(client, database, collection)
}
