package videoagent

import "eino-cli/videoagent/backend/workflow"

type (
	RunEvent    = workflow.RunEvent
	Monitor     = workflow.Monitor
	MonitorFunc = workflow.MonitorFunc
	Metrics     = workflow.Metrics
)

const (
	MonitorNodeStarted        = workflow.MonitorNodeStarted
	MonitorNodeWaiting        = workflow.MonitorNodeWaiting
	MonitorNodeCompleted      = workflow.MonitorNodeCompleted
	MonitorNodeFailed         = workflow.MonitorNodeFailed
	MonitorCallback           = workflow.MonitorCallback
	MonitorLeaseRenewalFailed = workflow.MonitorLeaseRenewalFailed
	MonitorSubmissionUnknown  = workflow.MonitorSubmissionUnknown
	MonitorReconcileFailed    = workflow.MonitorReconcileFailed
	MonitorRestoreFailed      = workflow.MonitorRestoreFailed
	MonitorCancelFailed       = workflow.MonitorCancelFailed
)

func NewMetrics() *Metrics {
	return workflow.NewMetrics()
}
