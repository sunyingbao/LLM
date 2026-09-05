package api

import (
	"fmt"

	"eino-cli/deepagent/coordinator"
)

type CoordinatorClientConfig struct {
	Coordinator *coordinator.Coordinator
	Namespace   string
	Env         string
}

func NewCoordinatorAPI(config CoordinatorClientConfig) (agentAPI *AgentAPI, err error) {
	if config.Coordinator == nil {
		return nil, fmt.Errorf("coordinator is required")
	}
	agentAPI = &AgentAPI{
		Namespace: config.Namespace,
		Env:       config.Env,
		Coordinator: CoordinatorAdapter{
			Namespace:   config.Namespace,
			Env:         config.Env,
			Coordinator: config.Coordinator,
		},
	}
	return agentAPI, nil
}
