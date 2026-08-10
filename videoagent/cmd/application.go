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

func newMessageBus(ctx context.Context, options runOptions) (*messaging.NATSMessageBus, error) {
	return messaging.NewNATSMessageBus(ctx, messaging.NATSConfig{
		URL:      options.natsURL,
		Stream:   options.natsStream,
		Subject:  options.natsSubject,
		Consumer: options.natsConsumer,
	})
}

func newApplication(ctx context.Context, options runOptions, credentials *videomodel.CredentialsConfig) (*app.Application, error) {
	if options.remoteConfigPath != "" {
		return newRemoteApplication(ctx, options, credentials)
	}
	return newLocalApplication(ctx, options, credentials)
}

func newLocalApplication(ctx context.Context, options runOptions, credentials *videomodel.CredentialsConfig) (*app.Application, error) {
	var (
		application *app.Application
		err         error
	)
	if options.mongoURI != "" {
		application, err = app.NewMongoLocalApplication(options.mongoURI, options.mongoDatabase, options.mongoCollection)
	} else {
		application, err = app.NewLocalApplication(options.dataDir)
	}
	if err != nil {
		return nil, err
	}

	chatModel, planner, err := loadModels(ctx, options, credentials)
	if err != nil {
		_ = application.Close()
		return nil, err
	}
	if err := application.ConfigureModels(chatModel, planner); err != nil {
		_ = application.Close()
		return nil, err
	}
	return application, nil
}

func newRemoteApplication(ctx context.Context, settings runOptions, credentials *videomodel.CredentialsConfig) (application *app.Application, err error) {
	if settings.modelConfigPath == "" && credentials == nil {
		return nil, errors.New("model config is required in remote mode")
	}
	if settings.promptConfigPath == "" && credentials == nil {
		return nil, errors.New("prompt config is required in remote mode")
	}
	remoteConfig, err := readJSON[media.RemoteConfig](settings.remoteConfigPath)
	if err != nil {
		return nil, err
	}
	if err := media.ValidateCanvasRemoteConfig(remoteConfig); err != nil {
		return nil, err
	}

	store := app.NewStore(settings.dataDir + "/workflow.json")
	var mongoClient *mongo.Client
	defer func() {
		if err != nil && mongoClient != nil {
			_ = mongoClient.Disconnect(context.Background())
		}
	}()
	if settings.mongoURI != "" {
		mongoClient, err = mongo.Connect(options.Client().ApplyURI(settings.mongoURI))
		if err != nil {
			return nil, err
		}
		pingContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = mongoClient.Ping(pingContext, readpref.Primary())
		cancel()
		if err != nil {
			return nil, err
		}
		store, err = app.NewMongoStore(mongoClient, settings.mongoDatabase, settings.mongoCollection)
		if err != nil {
			return nil, err
		}
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
