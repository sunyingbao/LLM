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

const defaultCanvasAgentModelKey = "aic.aic_tool.user_req_analysis"

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
	credentialsConfigPath := flag.String("credentials-config", "", "untracked JSON credentials for Fornax and MaaS models")
	chatModelKey := flag.String("chat-model-key", defaultCanvasAgentModelKey, "prompt key selecting the ReAct chat model credentials")
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
	var credentials *videoagent.CredentialsConfig
	if *credentialsConfigPath != "" {
		loaded, err := readJSON[videoagent.CredentialsConfig](*credentialsConfigPath)
		if err != nil {
			return err
		}
		credentials = &loaded
	}

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
		application, err := newRemoteApplication(ctx, *dataDir, *remoteConfigPath, *modelConfigPath, *promptConfigPath, *mongoURI, *mongoDatabase, *mongoCollection, *chatModelKey, credentials)
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
	if *modelConfigPath != "" || credentials != nil {
		chatModel, err := loadChatModel(*modelConfigPath, *chatModelKey, credentials)
		if err != nil {
			return err
		}
		if err := application.UseChatModel(chatModel); err != nil {
			return err
		}
	}
	if credentials != nil {
		planner, err := loadCredentialPlanner(ctx, *credentials)
		if err != nil {
			return err
		}
		if err := application.UsePlanner(planner); err != nil {
			return err
		}
	}
	if *promptConfigPath != "" {
		planner, err := loadPromptPlanner(*promptConfigPath, credentials)
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

func loadChatModel(path, promptKey string, credentials *videoagent.CredentialsConfig) (model.BaseChatModel, error) {
	if credentials != nil {
		config, err := credentials.ChatModelConfig(promptKey)
		if err != nil {
			return nil, err
		}
		return videoagent.NewChatModel(context.Background(), config)
	}
	config, err := readJSON[videoagent.ChatModelConfig](path)
	if err != nil {
		return nil, err
	}
	return videoagent.NewChatModel(context.Background(), config)
}

func loadCredentialPlanner(ctx context.Context, credentials videoagent.CredentialsConfig) (videoagent.Planner, error) {
	requirementModel, err := loadCredentialModel(ctx, credentials, "aic.aic_tool.user_req_analysis")
	if err != nil {
		return nil, err
	}
	clipScriptModel, err := loadCredentialModel(ctx, credentials, "jichuang.creative.dr_script_e2e")
	if err != nil {
		return nil, err
	}
	return videoagent.NewStageModelPlanner(requirementModel, clipScriptModel, requirementModel)
}

func loadCredentialModel(ctx context.Context, credentials videoagent.CredentialsConfig, promptKey string) (model.BaseChatModel, error) {
	config, err := credentials.ChatModelConfig(promptKey)
	if err != nil {
		return nil, err
	}
	return videoagent.NewChatModel(ctx, config)
}

func loadPromptPlanner(path string, credentials *videoagent.CredentialsConfig) (videoagent.Planner, error) {
	config := videoagent.PromptRuntimeConfig{Planner: videoagent.DefaultPromptPlannerConfig()}
	if path != "" {
		loaded, err := readJSON[videoagent.PromptRuntimeConfig](path)
		if err != nil {
			return nil, err
		}
		config = loaded
	}
	if credentials != nil {
		config.Fornax = credentials.Fornax
	}
	executor, err := videoagent.NewFornaxPromptExecutor(config.Fornax)
	if err != nil {
		return nil, err
	}
	return videoagent.NewPromptPlanner(executor, config.Planner)
}

func newRemoteApplication(ctx context.Context, dataDir, remoteConfigPath, modelConfigPath, promptConfigPath, mongoURI, mongoDatabase, mongoCollection, chatModelKey string, credentials *videoagent.CredentialsConfig) (application *videoagent.Application, err error) {
	if modelConfigPath == "" && credentials == nil {
		return nil, errors.New("model config is required in remote mode")
	}
	if promptConfigPath == "" && credentials == nil {
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
	chatModel, err := loadChatModel(modelConfigPath, chatModelKey, credentials)
	if err != nil {
		return nil, err
	}
	clients.Planner, err = videoagent.NewModelPlanner(chatModel)
	if err != nil {
		return nil, err
	}
	if credentials != nil {
		clients.Planner, err = loadCredentialPlanner(ctx, *credentials)
		if err != nil {
			return nil, err
		}
	}
	if promptConfigPath != "" {
		clients.Planner, err = loadPromptPlanner(promptConfigPath, credentials)
		if err != nil {
			return nil, err
		}
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
	application.Runner.SetMonitor(videoagent.MonitorFunc(func(_ context.Context, event videoagent.RunEvent) {
		log.Printf("video_agent action=%s run_id=%s node_id=%s kind=%s state=%s provider=%s duration_ms=%d message=%q",
			event.Action, event.RunID, event.NodeID, event.Kind, event.State, event.Provider, event.DurationMS, event.Message)
	}))
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
