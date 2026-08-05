package videoagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrSubmitReconciliationUnsupported means a provider cannot find an unknown submission by key.
var ErrSubmitReconciliationUnsupported = fmt.Errorf("submit-key reconciliation is unsupported")
var ErrCancellationUnsupported = fmt.Errorf("provider cancellation is unsupported")

type Requirement struct {
	Objective string   `json:"objective"`
	Audience  string   `json:"audience"`
	Selling   []string `json:"selling_points"`
	Markdown  string   `json:"markdown,omitempty"`
}

type Character struct {
	ID          string `json:"id"`
	Age         string `json:"age,omitempty"`
	Gender      string `json:"gender,omitempty"`
	Description string `json:"description,omitempty"`
}

type Scene struct {
	ID           string   `json:"id"`
	SemanticID   string   `json:"semantic_id,omitempty"`
	Voiceover    string   `json:"voiceover"`
	Visual       string   `json:"visual"`
	SpeakerID    string   `json:"speaker_id,omitempty"`
	CharacterIDs []string `json:"character_ids,omitempty"`
	PictureType  string   `json:"picture_type,omitempty"`
	Presentation string   `json:"presentation,omitempty"`
	DurationMS   int      `json:"duration_ms,omitempty"`
}

type ClipScript struct {
	Title        string          `json:"title"`
	CreativePlan json.RawMessage `json:"creative_plan,omitempty"`
	Characters   []Character     `json:"characters,omitempty"`
	Scenes       []Scene         `json:"scenes"`
}

type ResourcePlan struct {
	ID                   string          `json:"id"`
	SceneID              string          `json:"scene_id,omitempty"`
	ParentArtifactID     string          `json:"parent_artifact_id"`
	ArtifactIDs          []string        `json:"artifact_ids,omitempty"`
	Prompt               string          `json:"prompt,omitempty"`
	Speaker              string          `json:"speaker,omitempty"`
	Text                 string          `json:"text,omitempty"`
	ImageURLs            []string        `json:"image_urls,omitempty"`
	AudioURLs            []string        `json:"audio_urls,omitempty"`
	VideoURLs            []string        `json:"video_urls,omitempty"`
	ExistingVideoURI     string          `json:"existing_video_uri,omitempty"`
	ClipStartMS          int             `json:"clip_start_ms,omitempty"`
	ClipEndMS            int             `json:"clip_end_ms,omitempty"`
	OriginPictureClipIDs []int64         `json:"origin_picture_clip_ids,omitempty"`
	CutNumbers           []int32         `json:"cut_numbers,omitempty"`
	CutPlacements        []CutPlacement  `json:"cut_placements,omitempty"`
	Strategy             PreviewStrategy `json:"strategy,omitempty"`
	Model                string          `json:"model,omitempty"`
	FallbackModel        string          `json:"fallback_model,omitempty"`
	Width                int             `json:"width,omitempty"`
	Height               int             `json:"height,omitempty"`
	CPM                  int             `json:"cpm,omitempty"`
	Duration             int             `json:"duration,omitempty"`
	AspectRatio          string          `json:"aspect_ratio,omitempty"`
}

type CutPlacement struct {
	CutNumber      int32 `json:"cut_number"`
	ItemIndex      int   `json:"item_index"`
	CandidateIndex int   `json:"candidate_index"`
}

type NodeConfig struct {
	Instruction string `json:"instruction,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
	Model       string `json:"model,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	Duration    int    `json:"duration,omitempty"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
}

type Planner interface {
	AnalyzeRequirement(context.Context, RunInput) (Requirement, error)
	CreateClipScript(context.Context, Requirement, RunInput) (ClipScript, error)
	PlanCompetition(context.Context, ClipScript, RunInput) ([]ResourcePlan, error)
	PlanTTS(context.Context, ClipScript) ([]ResourcePlan, error)
	PlanCharacterReferences(context.Context, ClipScript, RunInput) ([]ResourcePlan, error)
}

type PreviewPlanner interface {
	PlanPreview(context.Context, ClipScript, RunInput, []Artifact) ([]ResourcePlan, error)
}

type PromptRequest struct {
	Key            string
	Text           string
	Variables      map[string]any
	ImageVariables map[string][]string
}

type PromptExecutor interface {
	Execute(context.Context, PromptRequest) (string, error)
}

type ImageRequest struct {
	Prompt    string
	ImageURLs []string
	Model     string
	Width     int
	Height    int
	SubmitKey string
}

type TTSRequest struct {
	Prompt      string
	Speaker     string
	Text        string
	CPM         int
	Model       string
	Async       bool
	WithExample bool
	SubmitKey   string
}

type MatxRequest struct {
	Model  string
	Bytes  map[string][][]byte
	Ints   map[string][]int64
	Floats map[string][]float64
}

type MatxResponse struct {
	Bytes map[string][][]byte
}

type MatxClient interface {
	Infer(context.Context, MatxRequest) (MatxResponse, error)
}

type ModelTaskRequest struct {
	Input     []byte
	Model     string
	TaskQueue string
	Extra     map[string]string
}

type ModelTaskStatus struct {
	Code       int32
	Status     string
	Result     []byte
	BizCode    int32
	BizMessage string
}

type ModelGateway interface {
	Generate(context.Context, ModelTaskRequest) ([]byte, error)
	CreateTask(context.Context, ModelTaskRequest) (string, error)
	GetTask(context.Context, string) (ModelTaskStatus, error)
}

type SubmittedJob struct {
	Provider string
	JobID    string
	Status   *JobStatus
}

type JobState string

const (
	JobPending   JobState = "pending"
	JobSucceeded JobState = "succeeded"
	JobFailed    JobState = "failed"
)

type JobStatus struct {
	State      JobState
	URI        string
	URL        string
	ExampleURI string
	ExampleURL string
	Message    string
	DurationMS int
}

type StoredVideo struct {
	URI string
	URL string
}

type VideoImporter interface {
	ImportVideo(context.Context, string, string) (StoredVideo, error)
}

type MediaURLResolver interface {
	ResolveURL(context.Context, string) (string, error)
}

type VideoImportCache interface {
	GetImportedVideo(context.Context, string) (StoredVideo, bool, error)
	SaveImportedVideo(context.Context, string, StoredVideo) error
}

type VideoUploader interface {
	UploadVideo(context.Context, io.Reader, int64) (string, error)
	SetVideoVisible(context.Context, string) error
}

type RenderScene struct {
	ID          string
	Source      string
	ClipStartMS int
	ClipEndMS   int
	DurationMS  int
	Speed       float64
}

type RenderAudio struct {
	Source     string
	StartMS    int
	DurationMS int
}

type RenderPlan struct {
	Width  int
	Height int
	Scenes []RenderScene
	Audios []RenderAudio
}

type VideoRenderer interface {
	StartRender(context.Context, RenderPlan) (string, error)
	GetRender(context.Context, string) (JobStatus, error)
}

type ImageClient interface {
	SubmitImage(context.Context, ImageRequest) (SubmittedJob, error)
	GetImage(context.Context, string) (JobStatus, error)
	FindImageBySubmitKey(context.Context, string) (SubmittedJob, bool, error)
}

type ImageCanceler interface {
	CancelImage(context.Context, string) error
}

type TTSClient interface {
	SubmitTTS(context.Context, TTSRequest) (SubmittedJob, error)
	GetTTS(context.Context, string) (JobStatus, error)
	FindTTSBySubmitKey(context.Context, string) (SubmittedJob, bool, error)
}

type TTSCanceler interface {
	CancelTTS(context.Context, string) error
}

type VideoRequest struct {
	Inputs               []Artifact
	ClipScript           *ClipScript
	Prompt               string
	Model                string
	Width                int
	Height               int
	AspectRatio          string
	Duration             int
	ImageURLs            []string
	AudioURLs            []string
	VideoURLs            []string
	Strategy             PreviewStrategy
	OriginPictureClipIDs []int64
	CutNumbers           []int32
	SubmitKey            string
}

type PreviewClient interface {
	SubmitPreview(context.Context, VideoRequest) (SubmittedJob, error)
	GetPreview(context.Context, string) (JobStatus, error)
	FindPreviewBySubmitKey(context.Context, string) (SubmittedJob, bool, error)
}

type FinalVideoClient interface {
	SubmitFinalVideo(context.Context, VideoRequest) (SubmittedJob, error)
	GetFinalVideo(context.Context, string) (JobStatus, error)
	FindFinalVideoBySubmitKey(context.Context, string) (SubmittedJob, bool, error)
}

type VideoClient interface {
	PreviewClient
	FinalVideoClient
}

type combinedVideoClient struct {
	PreviewClient
	FinalVideoClient
}

func CombineVideoClients(preview PreviewClient, final FinalVideoClient) (VideoClient, error) {
	if preview == nil || final == nil {
		return nil, fmt.Errorf("preview and final video clients are required")
	}
	return combinedVideoClient{PreviewClient: preview, FinalVideoClient: final}, nil
}

type VideoCanceler interface {
	CancelVideo(context.Context, string) error
}

func (client combinedVideoClient) cancelPreview(ctx context.Context, jobID string) error {
	if canceler, ok := client.PreviewClient.(VideoCanceler); ok {
		return canceler.CancelVideo(ctx, jobID)
	}
	return ErrCancellationUnsupported
}

func (client combinedVideoClient) cancelFinalVideo(ctx context.Context, jobID string) error {
	if canceler, ok := client.FinalVideoClient.(VideoCanceler); ok {
		return canceler.CancelVideo(ctx, jobID)
	}
	return ErrCancellationUnsupported
}

type ImageAuditor interface {
	CheckImage(context.Context, string) error
}

type PromptShield interface {
	CheckPrompt(context.Context, string) error
}

type Clients struct {
	Planner        Planner
	PreviewPlanner PreviewPlanner
	Image          ImageClient
	CharacterImage ImageClient
	TTS            TTSClient
	Video          VideoClient
	Audit          ImageAuditor
	Shield         PromptShield
}

func (clients Clients) validate() error {
	missing := make([]string, 0, 6)
	for _, dependency := range []struct {
		name      string
		available bool
	}{
		{"planner", clients.Planner != nil},
		{"image", clients.Image != nil},
		{"tts", clients.TTS != nil},
		{"video", clients.Video != nil},
		{"image_audit", clients.Audit != nil},
		{"prompt_shield", clients.Shield != nil},
	} {
		if !dependency.available {
			missing = append(missing, dependency.name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("video agent clients are incomplete: missing %s", strings.Join(missing, ", "))
}

type CallbackMessage struct {
	Provider  string `json:"provider"`
	EventID   string `json:"event_id"`
	JobID     string `json:"job_id,omitempty"`
	SubmitKey string `json:"submit_key,omitempty"`
}

// CallbackVerifier authenticates provider callbacks before they enter the runner.
type CallbackVerifier interface {
	Verify(context.Context, string, []byte, http.Header) error
}

type MessagePublisher interface {
	Publish(context.Context, CallbackMessage) error
}

// MessagePublisherFunc adapts a callback function to MessagePublisher.
type MessagePublisherFunc func(context.Context, CallbackMessage) error

func (publisher MessagePublisherFunc) Publish(ctx context.Context, message CallbackMessage) error {
	if publisher == nil {
		return fmt.Errorf("callback publisher is nil")
	}
	return publisher(ctx, message)
}

type MessageConsumer interface {
	Consume(context.Context, func(context.Context, CallbackMessage) error) error
}
