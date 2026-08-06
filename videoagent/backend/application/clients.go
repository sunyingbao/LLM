package application

import "eino-cli/videoagent/backend/contract"

type (
	Requirement          = contract.Requirement
	Character            = contract.Character
	Scene                = contract.Scene
	ClipScript           = contract.ClipScript
	ResourcePlan         = contract.ResourcePlan
	CutPlacement         = contract.CutPlacement
	NodeConfig           = contract.NodeConfig
	Planner              = contract.Planner
	PreviewPlanner       = contract.PreviewPlanner
	PromptRequest        = contract.PromptRequest
	PromptExecutor       = contract.PromptExecutor
	ImageRequest         = contract.ImageRequest
	TTSRequest           = contract.TTSRequest
	MatxRequest          = contract.MatxRequest
	MatxResponse         = contract.MatxResponse
	MatxClient           = contract.MatxClient
	ModelTaskRequest     = contract.ModelTaskRequest
	ModelTaskStatus      = contract.ModelTaskStatus
	ModelGateway         = contract.ModelGateway
	SubmittedJob         = contract.SubmittedJob
	JobState             = contract.JobState
	JobStatus            = contract.JobStatus
	StoredVideo          = contract.StoredVideo
	VideoImporter        = contract.VideoImporter
	MediaURLResolver     = contract.MediaURLResolver
	VideoImportCache     = contract.VideoImportCache
	VideoUploader        = contract.VideoUploader
	RenderScene          = contract.RenderScene
	RenderAudio          = contract.RenderAudio
	RenderPlan           = contract.RenderPlan
	VideoRenderer        = contract.VideoRenderer
	ImageClient          = contract.ImageClient
	ImageCanceler        = contract.ImageCanceler
	TTSClient            = contract.TTSClient
	TTSCanceler          = contract.TTSCanceler
	VideoRequest         = contract.VideoRequest
	PreviewClient        = contract.PreviewClient
	FinalVideoClient     = contract.FinalVideoClient
	VideoClient          = contract.VideoClient
	VideoCanceler        = contract.VideoCanceler
	ImageAuditor         = contract.ImageAuditor
	PromptShield         = contract.PromptShield
	Clients              = contract.Clients
	CallbackMessage      = contract.CallbackMessage
	CallbackVerifier     = contract.CallbackVerifier
	MessagePublisher     = contract.MessagePublisher
	MessageConsumer      = contract.MessageConsumer
	MessagePublisherFunc = contract.MessagePublisherFunc
	PreviewStrategy      = contract.PreviewStrategy
	CombinedVideoClient  = contract.CombinedVideoClient
)

const (
	JobPending   = contract.JobPending
	JobSucceeded = contract.JobSucceeded
	JobFailed    = contract.JobFailed

	PreviewStrategyT2V      = contract.PreviewStrategyT2V
	PreviewStrategyI2V      = contract.PreviewStrategyI2V
	PreviewStrategyR2V      = contract.PreviewStrategyR2V
	PreviewStrategyMaterial = contract.PreviewStrategyMaterial
)

var (
	ErrSubmitReconciliationUnsupported = contract.ErrSubmitReconciliationUnsupported
	ErrCancellationUnsupported         = contract.ErrCancellationUnsupported
)

func CombineVideoClients(preview PreviewClient, final FinalVideoClient) (VideoClient, error) {
	return contract.CombineVideoClients(preview, final)
}
