package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"eino-cli/deepagent/cloud/worker/bootstrap"
	apiapp "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk"
	apiconfig "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/config"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/deps"
	sessionapp "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session"
	sessionconfig "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session/config"
	"eino-cli/deepagent/coordinator"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) (err error) {
	workerConfig, err := bootstrap.LoadConfig(args)
	if err != nil {
		return err
	}
	core, err := coordinator.New(ctx, coordinator.Config{
		MySQLDSN:      workerConfig.MySQL.DSN,
		MySQLReadDSN:  workerConfig.MySQL.ReadDSN,
		RedisAddress:  workerConfig.Abase.Addr,
		RedisPassword: workerConfig.Abase.Password,
		RedisDB:       workerConfig.Abase.DB,
	})
	if err != nil {
		return fmt.Errorf("initialize coordinator: %w", err)
	}

	sessionConfig, err := sessionconfig.Load()
	if err != nil {
		return err
	}
	sessionConfig.MySQL.DSN = workerConfig.MySQL.DSN
	sessionConfig.MySQL.ReadDSN = workerConfig.MySQL.ReadDSN
	sessionConfig.CoordinatorNamespace = workerConfig.Worker.Namespace
	sessionService, closeSession, err := sessionapp.New(ctx, sessionConfig, core)
	if err != nil {
		return err
	}
	defer closeSession()

	deps.SetCoordinator(core)
	deps.SetSessionService(sessionService)
	apiConfig := apiconfig.Load()
	apiConfig.ACNamespace = workerConfig.Worker.Namespace
	apiConfig.Backend = workerConfig.Backend
	apiConfig.WorkspaceRoot = workerConfig.Backend.Local.Root
	deps.SetConfig(apiConfig)

	workerErrors := make(chan error, 1)
	go func() {
		workerErrors <- bootstrap.Run(ctx, bootstrap.Options{Args: args, Coordinator: core})
	}()

	server := apiapp.NewServer(os.Getenv("DEEP_AGENT_SDK_API_ADDRESS"))
	apiapp.Register(server)
	apiErrors := make(chan error, 1)
	go func() {
		apiErrors <- server.Run()
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	select {
	case <-ctx.Done():
		return nil
	case err = <-apiErrors:
		if err == nil {
			return nil
		}
		return fmt.Errorf("api server: %w", err)
	case err = <-workerErrors:
		if err == context.Canceled {
			return nil
		}
		return err
	}
}
