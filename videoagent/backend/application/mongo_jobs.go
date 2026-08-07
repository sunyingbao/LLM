package application

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const mongoJobsDocumentID = "video-agent-jobs"

type mongoJobDocument struct {
	ID   string       `bson:"_id"`
	Data localJobData `bson:"data"`
}

type mongoJobBackend struct {
	collection *mongo.Collection
}

// NewMongoJobs stores asynchronous task state in MongoDB.
func NewMongoJobs(client *mongo.Client, database, collection string) (*LocalJobs, error) {
	if client == nil {
		return nil, fmt.Errorf("mongo client is nil")
	}
	if database == "" || collection == "" {
		return nil, fmt.Errorf("mongo database and collection are required")
	}
	backend := &mongoJobBackend{collection: client.Database(database).Collection(collection)}
	if err := backend.init(); err != nil {
		return nil, err
	}
	return &LocalJobs{backend: backend}, nil
}

func (backend *mongoJobBackend) init() error {
	_, err := backend.collection.UpdateOne(
		context.Background(),
		bson.M{"_id": mongoJobsDocumentID},
		bson.M{"$setOnInsert": mongoJobDocument{ID: mongoJobsDocumentID, Data: emptyLocalJobData()}},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

func (backend *mongoJobBackend) Load() (localJobData, error) {
	var document mongoJobDocument
	err := backend.collection.FindOne(
		context.Background(),
		bson.M{"_id": mongoJobsDocumentID},
	).Decode(&document)
	if err != nil {
		return localJobData{}, err
	}
	return normalizeLocalJobData(document.Data), nil
}

func (backend *mongoJobBackend) Save(data localJobData) error {
	prevRev := data.Revision
	data.Revision++
	result, err := backend.collection.ReplaceOne(
		context.Background(),
		bson.M{"_id": mongoJobsDocumentID, "data.revision": prevRev},
		mongoJobDocument{ID: mongoJobsDocumentID, Data: normalizeLocalJobData(data)},
	)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return errJobStateConflict
	}
	return nil
}
