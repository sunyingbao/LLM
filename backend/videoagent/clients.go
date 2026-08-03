package videoagent

import "context"

type Requirement struct {
	Objective string   `json:"objective"`
	Audience  string   `json:"audience"`
	Selling   []string `json:"selling_points"`
}

type Scene struct {
	ID        string `json:"id"`
	Voiceover string `json:"voiceover"`
	Visual    string `json:"visual"`
}

type Storyboard struct {
	Title  string  `json:"title"`
	Scenes []Scene `json:"scenes"`
}

type ResourcePlan struct {
	ID               string   `json:"id"`
	ParentArtifactID string   `json:"parent_artifact_id"`
	Prompt           string   `json:"prompt,omitempty"`
	Speaker          string   `json:"speaker,omitempty"`
	Text             string   `json:"text,omitempty"`
	ImageURLs        []string `json:"image_urls,omitempty"`
	Model            string   `json:"model,omitempty"`
	FallbackModel    string   `json:"fallback_model,omitempty"`
}

type Planner interface {
	AnalyzeRequirement(context.Context, RunInput) (Requirement, error)
	CreateStoryboard(context.Context, Requirement) (Storyboard, error)
	PlanCompetition(context.Context, Storyboard, RunInput) ([]ResourcePlan, error)
	PlanTTS(context.Context, Storyboard) ([]ResourcePlan, error)
	PlanCharacterReferences(context.Context, Storyboard, RunInput) ([]ResourcePlan, error)
}

type ImageRequest struct {
	Prompt    string
	ImageURLs []string
	Model     string
	SubmitKey string
}

type TTSRequest struct {
	Speaker     string
	Text        string
	WithExample bool
	SubmitKey   string
}

type SubmittedJob struct {
	Provider string
	JobID    string
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
}

type ImageClient interface {
	SubmitImage(context.Context, ImageRequest) (SubmittedJob, error)
	GetImage(context.Context, string) (JobStatus, error)
	FindImageBySubmitKey(context.Context, string) (SubmittedJob, bool, error)
}

type TTSClient interface {
	SubmitTTS(context.Context, TTSRequest) (SubmittedJob, error)
	GetTTS(context.Context, string) (JobStatus, error)
	FindTTSBySubmitKey(context.Context, string) (SubmittedJob, bool, error)
}

type ImageAuditor interface {
	CheckImage(context.Context, string) error
}

type PromptShield interface {
	CheckPrompt(context.Context, string) error
}

type Clients struct {
	Planner Planner
	Image   ImageClient
	TTS     TTSClient
	Audit   ImageAuditor
	Shield  PromptShield
}
