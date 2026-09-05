package deps

import (
	"fmt"
	"sync"

	"code.byted.org/gdp/env"
	cloudapi "eino-cli/deepagent/cloud/api"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/config"
	sessionsvc "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session/service/session"
	"eino-cli/deepagent/coordinator"
)

var (
	coordinatorMu  sync.RWMutex
	shared         *coordinator.Coordinator
	sessionService *sessionsvc.Service
	apiConfig      *config.Config
)

func Config() (result config.Config) {
	coordinatorMu.RLock()
	configured := apiConfig
	coordinatorMu.RUnlock()
	if configured != nil {
		return *configured
	}
	return config.Load()
}

func SetConfig(value config.Config) {
	coordinatorMu.Lock()
	apiConfig = &value
	coordinatorMu.Unlock()
}

func SetCoordinator(value *coordinator.Coordinator) {
	coordinatorMu.Lock()
	shared = value
	coordinatorMu.Unlock()
}

func SetSessionService(value *sessionsvc.Service) {
	coordinatorMu.Lock()
	sessionService = value
	coordinatorMu.Unlock()
}

func SessionService() (service *sessionsvc.Service, err error) {
	coordinatorMu.RLock()
	service = sessionService
	coordinatorMu.RUnlock()
	if service == nil {
		return nil, fmt.Errorf("session service is not initialized")
	}
	return service, nil
}

func AgentAPI() (agentAPI *cloudapi.AgentAPI, err error) {
	coordinatorMu.RLock()
	core := shared
	coordinatorMu.RUnlock()
	if core == nil {
		return nil, fmt.Errorf("coordinator is not initialized")
	}
	cfg := Config()
	runtimeEnv := env.Env()
	return &cloudapi.AgentAPI{
		Namespace: cfg.ACNamespace,
		Env:       runtimeEnv,
		Coordinator: cloudapi.CoordinatorAdapter{
			Namespace:   cfg.ACNamespace,
			Env:         runtimeEnv,
			Coordinator: core,
		},
		DefaultLimit: cfg.TimelineDefaultLimit,
		MaxLimit:     cfg.TimelineMaxLimit,
	}, nil
}
