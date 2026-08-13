package sessionrpc

import (
	"fmt"

	"code.byted.org/kite/kitex/client"
	aicAgentSDKSessionSvc "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/kitex_gen/aic_agent_sdk_session/aicagentsdksessionservice"
)

type Client = aicAgentSDKSessionSvc.Client

func New(psm string, cluster string, hostPorts []string) (Client, error) {
	opts := make([]client.Option, 0, 2)
	if cluster != "" {
		opts = append(opts, client.WithCluster(cluster))
	}
	if len(hostPorts) > 0 {
		opts = append(opts, client.WithHostPorts(hostPorts...))
	}
	c, err := aicAgentSDKSessionSvc.NewClient(psm, opts...)
	if err != nil {
		return nil, fmt.Errorf("create aic_agent_sdk_session client: %w", err)
	}
	return c, nil
}
