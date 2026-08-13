package common

import (
	"encoding/json"
	"fmt"

	acbase "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/base"
	httpbase "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/base"
	sessionbase "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/kitex_gen/base"
)

func Convert[T any](src any) (T, error) {
	var dst T
	if src == nil {
		return dst, nil
	}
	data, err := json.Marshal(src)
	if err != nil {
		return dst, fmt.Errorf("marshal generated type: %w", err)
	}
	if err := json.Unmarshal(data, &dst); err != nil {
		return dst, fmt.Errorf("unmarshal generated type: %w", err)
	}
	return dst, nil
}

func BaseRespFromSession(resp *sessionbase.BaseResp) *httpbase.BaseResp {
	if resp == nil {
		return BaseRespOK()
	}
	return &httpbase.BaseResp{StatusCode: resp.GetStatusCode(), StatusMessage: resp.GetStatusMessage()}
}

func BaseRespFromAC(resp *acbase.BaseResp) *httpbase.BaseResp {
	if resp == nil {
		return BaseRespOK()
	}
	return &httpbase.BaseResp{StatusCode: resp.GetStatusCode(), StatusMessage: resp.GetStatusMessage()}
}

func CheckHTTPBaseResp(method string, resp *httpbase.BaseResp) error {
	if resp == nil || resp.StatusCode == 0 {
		return nil
	}
	return NewError(502, resp.StatusCode, method+" returned non-zero BaseResp: "+resp.StatusMessage, nil)
}
