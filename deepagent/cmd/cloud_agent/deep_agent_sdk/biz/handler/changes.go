package handler

import (
	"context"
	"net/http"

	"code.byted.org/middleware/hertz/pkg/app"
	"code.byted.org/middleware/hertz_ext/v2/binding"
	httpbase "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/hertz_gen/base"
	changesvc "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/changes"
	servicecommon "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/common"
)

type listChangesResponse struct {
	Changes  []changesvc.ChangeInfo `json:"changes"`
	BaseResp *httpbase.BaseResp     `json:"BaseResp"`
}

type getDiffResponse struct {
	Path      string             `json:"path"`
	Patch     string             `json:"patch"`
	Truncated bool               `json:"truncated"`
	BaseResp  *httpbase.BaseResp `json:"BaseResp"`
}

func ListChanges(ctx context.Context, c *app.RequestContext) {
	var req changesvc.ListRequest
	if err := binding.BindAndValidate(c, &req); err != nil {
		baseResp, status := commonBaseResp(err)
		c.JSON(status, &listChangesResponse{BaseResp: baseResp})
		return
	}
	ctx, uid, err := currentUID(ctx, c)
	if err != nil {
		baseResp, status := commonBaseResp(err)
		c.JSON(status, &listChangesResponse{BaseResp: baseResp})
		return
	}
	resp, err := changesvc.List(ctx, uid, &req)
	if err != nil {
		baseResp, status := commonBaseResp(err)
		c.JSON(status, &listChangesResponse{BaseResp: baseResp})
		return
	}
	c.JSON(http.StatusOK, &listChangesResponse{Changes: resp.Changes, BaseResp: servicecommon.BaseRespOK()})
}

func GetDiff(ctx context.Context, c *app.RequestContext) {
	var req changesvc.DiffRequest
	if err := binding.BindAndValidate(c, &req); err != nil {
		baseResp, status := commonBaseResp(err)
		c.JSON(status, &getDiffResponse{BaseResp: baseResp})
		return
	}
	ctx, uid, err := currentUID(ctx, c)
	if err != nil {
		baseResp, status := commonBaseResp(err)
		c.JSON(status, &getDiffResponse{BaseResp: baseResp})
		return
	}
	resp, err := changesvc.Diff(ctx, uid, &req)
	if err != nil {
		baseResp, status := commonBaseResp(err)
		c.JSON(status, &getDiffResponse{BaseResp: baseResp})
		return
	}
	c.JSON(http.StatusOK, &getDiffResponse{
		Path:      resp.Path,
		Patch:     resp.Patch,
		Truncated: resp.Truncated,
		BaseResp:  servicecommon.BaseRespOK(),
	})
}
