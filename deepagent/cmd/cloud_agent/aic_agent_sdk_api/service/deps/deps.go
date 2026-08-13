package deps

import (
	"sync"

	"code.byted.org/gdp/env"
	cloudapi "eino-cli/deepagent/cloud/api"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/infra/ac"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/infra/sessionrpc"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/service/config"
)

var (
	sessionOnce sync.Once
	sessionCli  sessionrpc.Client
	sessionErr  error

	acOnce sync.Once
	acCli  *ac.Clients
	acErr  error
)

func Config() config.Config {
	return config.Load()
}

func SessionClient() (sessionrpc.Client, error) {
	cfg := Config()
	sessionOnce.Do(func() {
		sessionCli, sessionErr = sessionrpc.New(cfg.AICAgentSDKSessionPSM, cfg.AICAgentSDKSessionCluster, cfg.AICAgentSDKSessionDirectHosts)
	})
	return sessionCli, sessionErr
}

func ACClients() (*ac.Clients, error) {
	cfg := Config()
	acOnce.Do(func() {
		acCli, acErr = ac.New(cfg.AgentCoordinatorPSM, cfg.ACCluster, cfg.ACDirectHostPorts)
	})
	return acCli, acErr
}

func AgentAPI() (*cloudapi.AgentAPI, error) {
	cfg := Config()
	clients, err := ACClients()
	if err != nil {
		return nil, err
	}
	runtimeEnv := env.Env()
	return &cloudapi.AgentAPI{
		Namespace: cfg.ACNamespace,
		Env:       runtimeEnv,
		Coordinator: cloudapi.CoordinatorAdapter{
			Namespace: cfg.ACNamespace,
			Env:       runtimeEnv,
			Client:    clients.RPC,
			Stream:    clients.Stream,
		},
		DefaultLimit: cfg.TimelineDefaultLimit,
		MaxLimit:     cfg.TimelineMaxLimit,
	}, nil
}
