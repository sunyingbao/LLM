package fornax

import (
	"context"
	"strconv"

	"code.byted.org/flowdevops/fornax_sdk"
	"code.byted.org/flowdevops/fornax_sdk/consts"
	"code.byted.org/flowdevops/fornax_sdk/infra/ctxmeta"
	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/worker/thread/runtimectx"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

const (
	tagSessionID     = "session_id"
	tagTurnID        = "turn_id"
	tagNamespace     = "namespace"
	tagCloudAgentEnv = "cloudagent_env"

	// The upstream Eino Fornax callback currently reports ToolsNode verbatim as
	// span_type=ToolsNode, which Fornax does not recognize. Keep the original
	// component tag after normalizing only the display type to graph.
	tagEinoRunInfoComponent = "eino_run_info_component"
)

// correlationHandler keeps the default Eino Fornax instrumentation intact and
// enriches every span with the CloudAgent identity carried by the run context.
// Wrapping the default handler is intentional: Eino does not propagate context
// mutations between separate callback handlers.
type correlationHandler struct {
	client   *fornax_sdk.Client
	delegate callbacks.Handler
}

var _ callbacks.Handler = (*correlationHandler)(nil)

func newCorrelationHandler(client *fornax_sdk.Client, delegate callbacks.Handler) callbacks.Handler {
	return &correlationHandler{client: client, delegate: delegate}
}

func (h *correlationHandler) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	isRoot := h.client.GetSpanFromContext(ctx) == nil
	ctx = h.delegate.OnStart(withCorrelation(ctx), normalizeRunInfoForFornax(info), input)
	h.restoreOriginalComponent(ctx, info)
	h.logRootTrace(ctx, isRoot)
	return ctx
}

func (h *correlationHandler) OnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	return h.delegate.OnEnd(ctx, normalizeRunInfoForFornax(info), output)
}

func (h *correlationHandler) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	return h.delegate.OnError(ctx, normalizeRunInfoForFornax(info), err)
}

func (h *correlationHandler) OnStartWithStreamInput(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	isRoot := h.client.GetSpanFromContext(ctx) == nil
	ctx = h.delegate.OnStartWithStreamInput(withCorrelation(ctx), normalizeRunInfoForFornax(info), input)
	h.restoreOriginalComponent(ctx, info)
	h.logRootTrace(ctx, isRoot)
	return ctx
}

func (h *correlationHandler) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	return h.delegate.OnEndWithStreamOutput(ctx, normalizeRunInfoForFornax(info), output)
}

func normalizeRunInfoForFornax(info *callbacks.RunInfo) *callbacks.RunInfo {
	if info == nil || info.Component != compose.ComponentOfToolsNode {
		return info
	}
	normalized := *info
	normalized.Component = compose.ComponentOfGraph
	return &normalized
}

func (h *correlationHandler) restoreOriginalComponent(ctx context.Context, info *callbacks.RunInfo) {
	if info == nil || info.Component != compose.ComponentOfToolsNode {
		return
	}
	span := h.client.GetSpanFromContext(ctx)
	if span == nil {
		return
	}
	span.SetTag(ctx, map[string]any{tagEinoRunInfoComponent: string(info.Component)})
}

func (h *correlationHandler) logRootTrace(ctx context.Context, isRoot bool) {
	if !isRoot {
		return
	}
	span := h.client.GetSpanFromContext(ctx)
	if span == nil {
		return
	}
	traceID := span.GetTraceInfo(ctx).TraceID
	if traceID == "" {
		return
	}
	thread, _ := runtimectx.ThreadIdentityFromContext(ctx)
	turn, _ := runtimectx.TurnIdentityFromContext(ctx)
	logs.CtxInfo(ctx,
		"[cloud_agent worker] fornax trace started: trace_id=%s session_id=%s thread_id=%s turn_id=%s message_id=%s",
		traceID, thread.SessionID, firstNonEmpty(thread.ThreadID, turn.ThreadID), turn.TurnID, turn.MessageID,
	)
}

func withCorrelation(ctx context.Context) context.Context {
	extras := make(map[string]string, 7)
	addExtra := func(key, value string) {
		if value != "" {
			extras[key] = value
		}
	}

	thread, threadOK := runtimectx.ThreadIdentityFromContext(ctx)
	turn, turnOK := runtimectx.TurnIdentityFromContext(ctx)
	if threadOK {
		addExtra(tagSessionID, thread.SessionID)
		addExtra(consts.FornaxThreadID, thread.ThreadID)
		addExtra(tagNamespace, thread.Namespace)
		addExtra(tagCloudAgentEnv, thread.Env)
		if thread.UserID != 0 {
			addExtra(consts.FornaxUserID, strconv.FormatInt(thread.UserID, 10))
		}
	}
	if turnOK {
		if _, exists := extras[consts.FornaxThreadID]; !exists {
			addExtra(consts.FornaxThreadID, turn.ThreadID)
		}
		addExtra(tagTurnID, turn.TurnID)
		addExtra(consts.FornaxMessageID, turn.MessageID)
	}
	if len(extras) == 0 {
		return ctx
	}
	return ctxmeta.WithExtras(ctx, extras)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
