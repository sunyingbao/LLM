package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"eino-cli/backend/videoagent"
	"github.com/cloudwego/eino/components/model"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	address := flag.String("addr", "127.0.0.1:18080", "HTTP listen address")
	dataDir := flag.String("data", ".video-agent", "local workflow data directory")
	remoteConfigPath := flag.String("remote-config", "", "JSON config for direct image/TTS/video clients")
	modelConfigPath := flag.String("model-config", "", "JSON config for the optional ReAct chat model")
	promptConfigPath := flag.String("prompt-config", "", "JSON config for managed workflow prompts")
	mongoURI := flag.String("mongo-uri", "mongodb://127.0.0.1:27017", "MongoDB URI; empty disables MongoDB state")
	mongoDatabase := flag.String("mongo-database", "video_agent", "MongoDB database name")
	mongoCollection := flag.String("mongo-collection", "workflow_state", "MongoDB state collection name")
	natsURL := flag.String("nats-url", videoagent.DefaultNATSURL, "NATS server URL")
	natsStream := flag.String("nats-stream", videoagent.DefaultNATSStream, "JetStream stream name")
	natsSubject := flag.String("nats-subject", videoagent.DefaultNATSSubject, "callback subject")
	natsConsumer := flag.String("nats-consumer", videoagent.DefaultNATSConsumer, "durable callback consumer")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	messageBus, err := videoagent.NewNATSMessageBus(ctx, videoagent.NATSConfig{
		URL:      *natsURL,
		Stream:   *natsStream,
		Subject:  *natsSubject,
		Consumer: *natsConsumer,
	})
	if err != nil {
		return err
	}
	defer messageBus.Close()

	if *remoteConfigPath != "" {
		application, err := newRemoteApplication(ctx, *dataDir, *remoteConfigPath, *modelConfigPath, *promptConfigPath, *mongoURI, *mongoDatabase, *mongoCollection)
		if err != nil {
			return err
		}
		application.SetMessagePublisher(messageBus)
		application.SetMessageConsumer(messageBus)
		defer application.Close()
		if err := application.Start(ctx); err != nil {
			return err
		}
		return serve(ctx, *address, videoagent.NewApplicationHTTPHandler(application))
	}

	var application *videoagent.LocalApplication
	if *mongoURI != "" {
		application, err = videoagent.NewMongoLocalApplication(*dataDir, *mongoURI, *mongoDatabase, *mongoCollection)
	} else {
		application, err = videoagent.NewLocalApplication(*dataDir)
	}
	if err != nil {
		return err
	}
	if *modelConfigPath != "" {
		chatModel, err := loadChatModel(*modelConfigPath)
		if err != nil {
			return err
		}
		if err := application.UseChatModel(chatModel); err != nil {
			return err
		}
	}
	if *promptConfigPath != "" {
		planner, err := loadPromptPlanner(*promptConfigPath)
		if err != nil {
			return err
		}
		if err := application.UsePlanner(planner); err != nil {
			return err
		}
	}
	application.SetMessageQueue(messageBus, messageBus)
	defer application.Close()
	if err := application.Start(ctx); err != nil {
		return err
	}
	return serve(ctx, *address, videoagent.NewHTTPHandler(application))
}

func serve(ctx context.Context, address string, handler http.Handler) error {
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.ListenAndServe() }()
	log.Printf("video agent listening on http://%s", address)

	select {
	case err := <-serverDone:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-serverDone
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func loadChatModel(path string) (model.BaseChatModel, error) {
	config, err := readJSON[videoagent.ChatModelConfig](path)
	if err != nil {
		return nil, err
	}
	return videoagent.NewChatModel(context.Background(), config)
}

func loadPromptPlanner(path string) (videoagent.Planner, error) {
	config, err := readJSON[videoagent.PromptRuntimeConfig](path)
	if err != nil {
		return nil, err
	}
	executor, err := videoagent.NewFornaxPromptExecutor(config.Fornax)
	if err != nil {
		return nil, err
	}
	return videoagent.NewPromptPlanner(executor, config.Planner)
}

func newRemoteApplication(ctx context.Context, dataDir, remoteConfigPath, modelConfigPath, promptConfigPath, mongoURI, mongoDatabase, mongoCollection string) (application *videoagent.Application, err error) {
	if modelConfigPath == "" {
		return nil, errors.New("model config is required in remote mode")
	}
	if promptConfigPath == "" {
		return nil, errors.New("prompt config is required in remote mode")
	}
	remoteConfig, err := readJSON[videoagent.RemoteConfig](remoteConfigPath)
	if err != nil {
		return nil, err
	}
	store := videoagent.NewStore(dataDir + "/workflow.json")
	var mongoClient *mongo.Client
	defer func() {
		if err != nil && mongoClient != nil {
			_ = mongoClient.Disconnect(context.Background())
		}
	}()
	if mongoURI != "" {
		mongoClient, err = mongo.Connect(options.Client().ApplyURI(mongoURI))
		if err != nil {
			return nil, err
		}
		pingContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = mongoClient.Ping(pingContext, readpref.Primary())
		cancel()
		if err != nil {
			return nil, err
		}
		store, err = videoagent.NewMongoStore(mongoClient, mongoDatabase, mongoCollection)
		if err != nil {
			return nil, err
		}
	}
	clients, err := videoagent.NewRemoteClients(remoteConfig, store)
	if err != nil {
		return nil, err
	}
	chatModel, err := loadChatModel(modelConfigPath)
	if err != nil {
		return nil, err
	}
	clients.Planner, err = loadPromptPlanner(promptConfigPath)
	if err != nil {
		return nil, err
	}
	agent, err := videoagent.NewCanvasAgent(chatModel, store)
	if err != nil {
		return nil, err
	}
	if err := videoagent.EnsureProject(ctx, store, "demo"); err != nil {
		return nil, err
	}
	application, err = videoagent.NewApplication(store, clients, agent)
	if err != nil {
		return nil, err
	}
	var closeResources func() error
	if mongoClient != nil {
		closeResources = func() error { return mongoClient.Disconnect(context.Background()) }
	}
	application.SetCallbackVerifier(videoagent.HMACCallbackVerifier{Secret: remoteConfig.CallbackSecret})
	application.SetJobPollInterval(2 * time.Second)
	application.SetClose(closeResources)
	return application, nil
}

func readJSON[T any](path string) (T, error) {
	var value T
	payload, err := os.ReadFile(path)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return value, err
	}
	return value, nil
}
