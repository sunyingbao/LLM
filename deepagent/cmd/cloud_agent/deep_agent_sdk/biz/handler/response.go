package handler

import (
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/hertz_gen/base"
	apisvc "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/common"
)

func commonBaseResp(err error) (*base.BaseResp, int) {
	return apisvc.BaseRespFromError(err)
}
