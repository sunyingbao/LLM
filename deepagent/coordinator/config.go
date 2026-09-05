package coordinator

import "time"

type Config struct {
	MySQLDSN                string
	MySQLReadDSN            string
	RedisAddress            string
	RedisPassword           string
	RedisDB                 int
	SubscribeSessionMaxIdle time.Duration
}

const (
	defaultLeaseDuration  = 60 * time.Second
	maxLeaseDuration      = 30 * time.Minute
	defaultScanLimit      = int32(50)
	maxScanLimit          = int32(100)
	defaultCreatedByValue = "system"

	claimReasonExpiredRunningLease = "reclaimed_expired_running_lease"

	// 首次失败立即重试；连续失败推迟扫描，避免认领和失败形成热循环。
	defaultFailureReleaseBackoff = 3 * time.Second
)

// 只对已知 Worker 失败原因退避，未知原因仍可立即重试。
var defaultFailureReleaseReasons = map[string]struct{}{
	"agent thread failed":               {}, // SDK defaultErrorReleaseReason
	"agent thread build failed":         {}, // SDK buildThreadFailedReason
	"agent thread init failed":          {}, // SDK initThreadFailedReason
	"agent thread post message failed":  {}, // SDK postMessageFailedReason
	"agent thread ack failed":           {}, // SDK ackMessageFailedReason
	"agent thread control input failed": {}, // SDK controlInputFailedReason
	"agent thread interrupt failed":     {}, // SDK interruptFailedReason
	"agent thread not buildable":        {}, // SDK threadNotBuildableReason
	"mailbox redis load failed":         {}, // ClaimThread 输入读取失败
}

const (
	defaultListLimit = int32(100)
	maxListLimit     = int32(1000)

	MessageLookupReasonNotFound          = "not_found"
	MessageLookupReasonNamespaceMismatch = "namespace_mismatch"
	MessageLookupReasonThreadMismatch    = "thread_mismatch"
	MessageLookupReasonInvalidRef        = "invalid_ref"

	DefaultCancelInputReason = "user_cancel"
	DefaultCloseThreadReason = "user_close"
)

func normalizeLeaseDuration(leaseMS int64) (duration time.Duration) {
	if leaseMS <= 0 {
		return defaultLeaseDuration
	}
	duration = time.Duration(leaseMS) * time.Millisecond
	if duration > maxLeaseDuration {
		return maxLeaseDuration
	}
	return duration
}
