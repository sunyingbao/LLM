package application

import (
	"context"
	"fmt"
)

// LocalClients implements model and media boundaries with deterministic local results.
type LocalClients struct {
	jobs  *LocalJobs
	queue *LocalQueue
}

func (*LocalClients) AnalyzeRequirement(_ context.Context, input RunInput) (Requirement, error) {
	markdown := fmt.Sprintf("# 需求分析\n\n商品：%s\n\n%s", input.ProductName, input.Brief)
	return Requirement{
		Objective: input.Brief,
		Audience:  "interested shoppers",
		Selling:   []string{input.ProductName, "comfortable", "easy to style"},
		Markdown:  markdown,
	}, nil
}

func (*LocalClients) CreateClipScript(_ context.Context, requirement Requirement, _ RunInput) (ClipScript, error) {
	return ClipScript{Title: requirement.Objective, Scenes: []Scene{{ID: "scene-1", Voiceover: "Show the product benefit", Visual: "Product close-up"}, {ID: "scene-2", Voiceover: "Invite the viewer to act", Visual: "Lifestyle usage"}}}, nil
}

func (*LocalClients) PlanCompetition(_ context.Context, clipScript ClipScript, _ RunInput) ([]ResourcePlan, error) {
	return []ResourcePlan{{ID: "competition-1", SceneID: clipScript.Scenes[0].ID, Prompt: clipScript.Scenes[0].Visual, Model: "local-image"}}, nil
}

func (*LocalClients) PlanTTS(_ context.Context, clipScript ClipScript) ([]ResourcePlan, error) {
	plans := make([]ResourcePlan, 0, len(clipScript.Scenes))
	for _, scene := range clipScript.Scenes {
		plans = append(plans, ResourcePlan{ID: "voice-" + scene.ID, SceneID: scene.ID, Speaker: "local-narrator", Text: scene.Voiceover})
	}
	return plans, nil
}

func (*LocalClients) PlanCharacterReferences(_ context.Context, clipScript ClipScript, _ RunInput) ([]ResourcePlan, error) {
	scene := clipScript.Scenes[len(clipScript.Scenes)-1]
	return []ResourcePlan{{ID: "character-1", SceneID: scene.ID, Prompt: scene.Visual, Model: "local-image", FallbackModel: "local-image-fallback"}}, nil
}

func (clients *LocalClients) SubmitImage(_ context.Context, request ImageRequest) (SubmittedJob, error) {
	return clients.submit(CompetitionReferenceNode, request.SubmitKey)
}

func (clients *LocalClients) GetImage(_ context.Context, jobID string) (JobStatus, error) {
	return clients.jobs.Status(jobID)
}

func (clients *LocalClients) CancelImage(_ context.Context, jobID string) error {
	return clients.jobs.Cancel(jobID)
}

func (clients *LocalClients) FindImageBySubmitKey(_ context.Context, key string) (SubmittedJob, bool, error) {
	return clients.jobs.Find(key)
}

func (clients *LocalClients) SubmitTTS(_ context.Context, request TTSRequest) (SubmittedJob, error) {
	return clients.submit(PromptTTSNode, request.SubmitKey)
}

func (clients *LocalClients) GetTTS(_ context.Context, jobID string) (JobStatus, error) {
	return clients.jobs.Status(jobID)
}

func (clients *LocalClients) CancelTTS(_ context.Context, jobID string) error {
	return clients.jobs.Cancel(jobID)
}

func (clients *LocalClients) FindTTSBySubmitKey(_ context.Context, key string) (SubmittedJob, bool, error) {
	return clients.jobs.Find(key)
}

func (clients *LocalClients) SubmitPreview(_ context.Context, request VideoRequest) (SubmittedJob, error) {
	return clients.submit(PreviewNode, request.SubmitKey)
}

func (clients *LocalClients) GetPreview(_ context.Context, jobID string) (JobStatus, error) {
	return clients.jobs.Status(jobID)
}

func (clients *LocalClients) CancelVideo(_ context.Context, jobID string) error {
	return clients.jobs.Cancel(jobID)
}

func (clients *LocalClients) FindPreviewBySubmitKey(_ context.Context, key string) (SubmittedJob, bool, error) {
	return clients.jobs.Find(key)
}

func (clients *LocalClients) SubmitFinalVideo(_ context.Context, request VideoRequest) (SubmittedJob, error) {
	return clients.submit(FinalVideoNode, request.SubmitKey)
}

func (clients *LocalClients) GetFinalVideo(_ context.Context, jobID string) (JobStatus, error) {
	return clients.jobs.Status(jobID)
}

func (clients *LocalClients) FindFinalVideoBySubmitKey(_ context.Context, key string) (SubmittedJob, bool, error) {
	return clients.jobs.Find(key)
}

func (*LocalClients) CheckImage(context.Context, string) error { return nil }

func (*LocalClients) CheckPrompt(context.Context, string) error { return nil }

func (clients *LocalClients) submit(kind NodeKind, submitKey string) (SubmittedJob, error) {
	job, created, err := clients.jobs.Submit(kind, submitKey)
	if err != nil {
		return SubmittedJob{}, err
	}
	if created && clients.queue != nil {
		clients.queue.Enqueue(job.ID)
	}
	return submittedJob(job), nil
}
