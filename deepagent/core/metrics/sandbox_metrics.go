package metrics

import (
	"context"
	"strconv"
	"time"

	"code.byted.org/gopkg/metricx"
)

// SandboxFileWatchEmit 记录 sandbox_file_watch_mw 在不同口径下的调用次数和耗时。
//
// Tags:
//   - scope: 统计口径，overall / file_watch / handle_file_event_func
//   - biz_type: sandbox biz 类型
//   - errcode: 错误码，成功时为 0
//   - file_event_cnt_bucket_l: file event 数量分桶下界
//   - file_event_cnt_bucket_r: file event 数量分桶上界
func SandboxFileWatchEmit(ctx context.Context, scope string, bizType string, errorCode int32, fileEventCnt int, latency time.Duration) {
	if latency == 0 {
		return
	}

	if scope == "" {
		scope = "unknown"
	}
	if bizType == "" {
		bizType = "unknown"
	}
	bucketL, bucketR := sandboxFileEventBucketRange(fileEventCnt)
	tags := []metricx.T{
		{Name: "scope", Value: scope},
		{Name: "biz_type", Value: bizType},
		{Name: "errcode", Value: strconv.FormatInt(int64(errorCode), 10)},
		{Name: "file_event_cnt_bucket_l", Value: bucketL},
		{Name: "file_event_cnt_bucket_r", Value: bucketR},
	}
	agentMetricsInst.SandboxFileWatch.DeltaCounter.Inc(1, tags...)
	agentMetricsInst.SandboxFileWatch.Latency.Record(latency, tags...)
}

func sandboxFileEventBucketRange(cnt int) (string, string) {
	switch {
	case cnt <= 0:
		return "0", "0"
	case cnt <= 10:
		return "1", "10"
	case cnt <= 100:
		return "11", "100"
	case cnt <= 500:
		return "101", "500"
	default:
		return "501", "inf"
	}
}
