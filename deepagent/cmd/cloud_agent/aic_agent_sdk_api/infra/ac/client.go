package ac

import (
	"fmt"

	"code.byted.org/kite/kitex/client"
	"code.byted.org/kite/kitex/client/bstreamclient"
	acsvc "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator/agentcoordinatorservice"
	"github.com/cloudwego/kitex/client/streamclient"
)

type Clients struct {
	RPC    acsvc.Client
	Stream acsvc.StreamClient
}

func New(psm string, cluster string, hostPorts []string) (*Clients, error) {
	clientOpts := make([]client.Option, 0, 2)
	streamOpts := make([]streamclient.Option, 0, 2)
	if cluster != "" {
		clientOpts = append(clientOpts, client.WithCluster(cluster))
		streamOpts = append(streamOpts, bstreamclient.WithCluster(cluster))
	}
	if len(hostPorts) > 0 {
		clientOpts = append(clientOpts, client.WithHostPorts(hostPorts...))
		streamOpts = append(streamOpts, streamclient.WithHostPorts(hostPorts...))
	}
	rpc, err := acsvc.NewClient(psm, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("create Agent Coordinator RPC client: %w", err)
	}
	stream, err := acsvc.NewStreamClient(psm, streamOpts...)
	if err != nil {
		return nil, fmt.Errorf("create Agent Coordinator stream client: %w", err)
	}
	return &Clients{RPC: rpc, Stream: stream}, nil
}
