package application

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// NewMongoLocalApplication runs the deterministic local clients on MongoDB state.
func NewMongoLocalApplication(uri, database, collection string) (*Application, error) {
	if uri == "" || database == "" || collection == "" {
		return nil, fmt.Errorf("mongo uri, database and collection are required")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}
	pingContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = client.Ping(pingContext, readpref.Primary())
	cancel()
	if err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	store, err := NewMongoStore(client, database, collection)
	if err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	jobs, err := NewMongoJobs(client, database, collection+"_jobs")
	if err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	application, err := newLocalApplication(store, jobs, func() error {
		return client.Disconnect(context.Background())
	})
	if err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	return application, nil
}
