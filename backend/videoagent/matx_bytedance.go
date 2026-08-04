//go:build bytedance

package videoagent

import (
	"context"
	"fmt"

	"code.byted.org/overpass/ad_genai_lb_proxy/kitex_gen/lagrange/laplace"
	"code.byted.org/overpass/ad_genai_lb_proxy/rpc/ad_genai_lb_proxy"
)

type bytedanceMatxClient struct{}

func NewBytedanceMatxClient() (MatxClient, error) {
	return bytedanceMatxClient{}, nil
}

func (bytedanceMatxClient) Infer(ctx context.Context, request MatxRequest) (MatxResponse, error) {
	response, err := ad_genai_lb_proxy.RawCall.MatxInference(ctx, &laplace.MatxInferenceRequest{
		ModelName:       request.Model,
		InputBytesLists: request.Bytes,
		InputIntLists:   request.Ints,
		InputFloatLists: request.Floats,
	})
	if err != nil {
		return MatxResponse{}, err
	}
	if response == nil {
		return MatxResponse{}, fmt.Errorf("matx inference returned an empty response")
	}
	return MatxResponse{Bytes: response.OutputBytesLists}, nil
}

var _ MatxClient = bytedanceMatxClient{}
