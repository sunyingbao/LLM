package input

import (
	"context"
	"testing"

	"code.byted.org/kite/kitutil"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	httpapi "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/aic_agent_sdk_api"
	httpcommon "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/aic_agent_sdk_common"
)

func TestEnsureRequestLogIDGeneratesWhenMissing(t *testing.T) {
	ctx, logID := ensureRequestLogID(context.Background())
	if logID == "" {
		t.Fatalf("logID is empty")
	}
	got, ok := kitutil.GetCtxLogID(ctx)
	if !ok || got != logID {
		t.Fatalf("ctx logID=%q ok=%t, want %q", got, ok, logID)
	}
}

func TestEnsureRequestLogIDReusesExisting(t *testing.T) {
	ctx := kitutil.NewCtxWithLogID(context.Background(), "existing-logid")
	gotCtx, logID := ensureRequestLogID(ctx)
	if gotCtx != ctx {
		t.Fatalf("context should be reused when logid already exists")
	}
	if logID != "existing-logid" {
		t.Fatalf("logID=%q, want existing-logid", logID)
	}
}

func TestBuildMessageMetadataCarriesLogIDAndMode(t *testing.T) {
	mode := httpcommon.InputMode_IMPL_PLAN
	metadata := buildMessageMetadata("request-logid", &httpapi.SubmitInputHTTPRequest{Mode: &mode})
	if metadata[protoinput.MetadataLogID] != "request-logid" {
		t.Fatalf("metadata logid=%q, want request-logid", metadata[protoinput.MetadataLogID])
	}
	if metadata[protoinput.MetadataTurnMode] != protoinput.TurnModePlan {
		t.Fatalf("metadata turn_mode=%q, want %q", metadata[protoinput.MetadataTurnMode], protoinput.TurnModePlan)
	}
}
