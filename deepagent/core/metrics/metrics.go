package metrics

import (
	"sync"
	"time"

	"code.byted.org/gopkg/env"
	"code.byted.org/gopkg/metricx"
	"code.byted.org/gopkg/metricx/contrib/bytedmetrics"
)

var (
	initOnce sync.Once

	agentMetricsInst = &agentMetrics{}
)

type agentMetrics struct {
	SandboxFileWatch Wrapper `prefix:"sandbox_file_watch" tnames:"scope,biz_type,errcode,file_event_cnt_bucket_l,file_event_cnt_bucket_r"`
}
type Wrapper struct {
	DeltaCounter metricx.DeltaCounter `metric:"counter"`    // 计数
	Latency      metricx.Timer        `metric:"latency.us"` // 延迟统计
}

func MustInitOnce() {
	initOnce.Do(func() {
		metricx.MustInit(
			agentMetricsInst,
			metricx.WithFactories(bytedmetrics.Factory()),
			metricx.WithNamespace(env.PSM()+".deepagents"),
			metricx.WithTimeUnit(time.Microsecond),
		)
	})
}
