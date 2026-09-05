package handler

import (
	"context"

	"code.byted.org/gopkg/logs"
	"code.byted.org/middleware/hertz/pkg/app"
	"code.byted.org/middleware/hertz_ext/v2/binding"
	httpapi "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/hertz_gen/deep_agent_sdk"
	httpbase "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/hertz_gen/base"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/infra/passport"
	servicecommon "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/common"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/deps"
	apisession "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/session"
)

func currentUID(ctx context.Context, c *app.RequestContext) (context.Context, int64, error) {
	identity, err := passport.GetIdentity(c)
	if err == nil {
		logRequestIdentity(ctx, identity)
		return apisession.WithUserEmail(ctx, identity.Email), identity.UserID, nil
	}
	cfg := deps.Config()
	if cfg.UseLocalDefaultUIDOnAuthErr && cfg.LocalDefaultUID > 0 {
		identity = passport.Identity{UserID: cfg.LocalDefaultUID, EmployeeNumber: cfg.LocalDefaultUID}
		logRequestIdentity(ctx, identity)
		return ctx, cfg.LocalDefaultUID, nil
	}
	return ctx, 0, servicecommon.Unauthenticated(err)
}

func logRequestIdentity(ctx context.Context, identity passport.Identity) {
	logs.CtxInfo(ctx, "[deep_agent_sdk] request user identity: user_name=%s email=%s employee_number=%d",
		identity.UserName, identity.Email, identity.EmployeeNumber)
}

func writeBindError(c *app.RequestContext, err error) bool {
	if err == nil {
		return false
	}
	resp := &httpapi.CreateSessionHTTPResponse{}
	writeError(c, resp, servicecommon.InvalidArgument(err.Error()))
	return true
}

func writeJSON(c *app.RequestContext, resp any) {
	if err := binding.WriteHeader(c, resp); err != nil {
		c.String(500, err.Error())
		return
	}
	c.JSON(200, resp)
}

func writeError(c *app.RequestContext, resp any, err error) {
	baseResp, status := servicecommon.BaseRespFromError(err)
	setBaseResp(resp, baseResp)
	if err := binding.WriteHeader(c, resp); err != nil {
		c.String(500, err.Error())
		return
	}
	c.JSON(status, resp)
}

func setBaseResp(resp any, baseResp *httpbase.BaseResp) {
	switch r := resp.(type) {
	case *httpapi.CreateSessionHTTPResponse:
		r.BaseResp = baseResp
	case *httpapi.ListSessionsHTTPResponse:
		r.BaseResp = baseResp
	case *httpapi.ListProjectsHTTPResponse:
		r.BaseResp = baseResp
	case *httpapi.CloseProjectHTTPResponse:
		r.BaseResp = baseResp
	case *httpapi.GetSessionHTTPResponse:
		r.BaseResp = baseResp
	case *httpapi.UpdateSessionHTTPResponse:
		r.BaseResp = baseResp
	case *httpapi.CloseSessionHTTPResponse:
		r.BaseResp = baseResp
	case *httpapi.SubmitInputHTTPResponse:
		r.BaseResp = baseResp
	case *httpapi.StopRunningHTTPResponse:
		r.BaseResp = baseResp
	case *httpapi.ListTimelineHTTPResponse:
		r.BaseResp = baseResp
	case *httpapi.SubscribeTimelineHTTPResponse:
		r.BaseResp = baseResp
	}
}
