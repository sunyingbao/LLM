package main

import (
	"context"
	"errors"
	"log"
	"time"

	app "eino-cli/videoagent/backend/application"
	"eino-cli/videoagent/backend/media"
	"eino-cli/videoagent/backend/messaging"
	videomodel "eino-cli/videoagent/backend/model"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

const (
	defaultMongoURI        = "mongodb://127.0.0.1:27017"
	defaultMongoDatabase   = "video_agent"
	defaultMongoCollection = "workflow_state"
)

func newMessageBus(ctx context.Context) (*messaging.NATSMessageBus, error) {
	return messaging.NewNATSMessageBus(ctx, messaging.NATSConfig{
		URL: messaging.DefaultNATSURL,
	})
}

func newApplication(ctx context.Context, settings runOptions, credentials *videomodel.CredentialsConfig) (application *app.Application, err error) {
	if settings.remoteConfigPath == "" {
		return nil, errors.New("remote config is required")
	}
	if settings.modelConfigPath == "" && credentials == nil {
		return nil, errors.New("model config is required")
	}
	if settings.promptConfigPath == "" && credentials == nil {
		return nil, errors.New("prompt config is required")
	}
	remoteConfig, err := readJSON[media.RemoteConfig](settings.remoteConfigPath)
	if err != nil {
		return nil, err
	}
	if err := media.ValidateCanvasRemoteConfig(remoteConfig); err != nil {
		return nil, err
	}

	mongoClient, err := mongo.Connect(options.Client().ApplyURI(defaultMongoURI))
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = mongoClient.Disconnect(context.Background())
		}
	}()
	pingContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = mongoClient.Ping(pingContext, readpref.Primary())
	cancel()
	if err != nil {
		return nil, err
	}
	store, err := app.NewMongoStore(mongoClient, defaultMongoDatabase, defaultMongoCollection)
	if err != nil {
		return nil, err
	}

	clients, err := media.NewRemoteClients(remoteConfig, store)
	if err != nil {
		return nil, err
	}
	chatModel, planner, err := loadModels(ctx, settings, credentials)
	if err != nil {
		return nil, err
	}
	clients.Planner = planner
	if err := app.EnsureProject(ctx, store, "demo"); err != nil {
		return nil, err
	}

	application, err = app.NewApplication(store, clients)
	if err != nil {
		return nil, err
	}
	if err = application.ConfigureModels(chatModel, clients.Planner); err != nil {
		return nil, err
	}
	application.Runner.SetMonitor(app.MonitorFunc(func(_ context.Context, event app.RunEvent) {
		log.Printf("video_agent action=%s run_id=%s node_id=%s kind=%s state=%s provider=%s duration_ms=%d message=%q",
			event.Action, event.RunID, event.NodeID, event.Kind, event.State, event.Provider, event.DurationMS, event.Message)
	}))
	var closeResources func() error
	if mongoClient != nil {
		closeResources = func() error { return mongoClient.Disconnect(context.Background()) }
	}
	application.SetCallbackVerifier(messaging.HMACCallbackVerifier{Secret: remoteConfig.CallbackSecret})
	application.SetJobPollInterval(2 * time.Second)
	application.SetClose(closeResources)
	return application, nil
}

func logCloseError(name string, close func() error) {
	if err := close(); err != nil {
		log.Printf("close %s: %v", name, err)
	}
}
