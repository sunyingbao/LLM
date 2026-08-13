//go:build !windows

package cloud

import (
	"context"
	"encoding/json"
	"strings"

	"code.byted.org/gopkg/metainfo"
	"code.byted.org/kite/kitutil"
	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
)

const (
	metadataKeyBytedCtxMetaInfo = "byted_ctx_meta_info"
	metadataKeyKEnv             = "K_ENV"
)

func contextWithMessageRequestMeta(ctx context.Context, message *ac.Message) context.Context {
	if message == nil {
		return ctx
	}
	return contextWithRequestMeta(ctx, message.GetMetadata(), true)
}

func contextWithThreadRequestMeta(ctx context.Context, thread *ac.Thread) context.Context {
	if thread == nil {
		return ctx
	}
	return contextWithRequestMeta(ctx, thread.GetMetadata(), true)
}

func contextWithMessageLogMeta(ctx context.Context, message *ac.Message) context.Context {
	if message == nil {
		return ctx
	}
	return contextWithRequestMeta(ctx, message.GetMetadata(), false)
}

func contextWithThreadLogMeta(ctx context.Context, thread *ac.Thread) context.Context {
	if thread == nil {
		return ctx
	}
	return contextWithRequestMeta(ctx, thread.GetMetadata(), false)
}

func contextWithRequestMeta(ctx context.Context, metadata map[string]string, restoreEnv bool) context.Context {
	if restoreEnv {
		env := strings.TrimSpace(metadata[metadataKeyKEnv])
		if env != "" {
			ctx = kitutil.NewCtxWithEnv(ctx, env)
		}
	}
	raw := strings.TrimSpace(metadata[metadataKeyBytedCtxMetaInfo])
	if raw == "" {
		return ctx
	}
	values := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return ctx
	}
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		ctx = metainfo.WithPersistentValue(ctx, key, value)
	}
	return ctx
}
