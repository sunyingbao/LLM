//go:build !windows

package api

import (
	"fmt"

	"code.byted.org/kite/kitex/client"
	"code.byted.org/kite/kitex/client/bstreamclient"
	acsvc "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator/agentcoordinatorservice"
	"github.com/cloudwego/kitex/client/streamclient"
)

type CoordinatorClientConfig struct {
	PSM       string
	Cluster   string
	HostPorts []string
	Namespace string
	Env       string
}

func NewCoordinatorAPI(config CoordinatorClientConfig) (agentAPI *AgentAPI, err error) {
	if config.PSM == "" {
		return nil, fmt.Errorf("agent coordinator psm is required")
	}
	clientOptions := make([]client.Option, 0, 2)
	streamOptions := make([]streamclient.Option, 0, 2)
	if config.Cluster != "" {
		clientOptions = append(clientOptions, client.WithCluster(config.Cluster))
		streamOptions = append(streamOptions, bstreamclient.WithCluster(config.Cluster))
	}
	if len(config.HostPorts) > 0 {
		clientOptions = append(clientOptions, client.WithHostPorts(config.HostPorts...))
		streamOptions = append(streamOptions, streamclient.WithHostPorts(config.HostPorts...))
	}
	rpcClient, err := acsvc.NewClient(config.PSM, clientOptions...)
	if err != nil {
		return nil, fmt.Errorf("create Agent Coordinator RPC client: %w", err)
	}
	streamClient, err := acsvc.NewStreamClient(config.PSM, streamOptions...)
	if err != nil {
		return nil, fmt.Errorf("create Agent Coordinator stream client: %w", err)
	}
	agentAPI = &AgentAPI{
		Namespace: config.Namespace,
		Env:       config.Env,
		Coordinator: CoordinatorAdapter{
			Namespace: config.Namespace, Env: config.Env, Client: rpcClient, Stream: streamClient,
		},
	}
	return agentAPI, nil
}
