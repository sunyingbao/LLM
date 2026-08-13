package handler

import (
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/base"
	apisvc "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/service/common"
)

func commonBaseResp(err error) (*base.BaseResp, int) {
	return apisvc.BaseRespFromError(err)
}
