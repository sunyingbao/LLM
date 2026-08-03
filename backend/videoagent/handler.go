package videoagent

import (
	"context"
	"encoding/json"
	"fmt"
)

type nodeHandler struct {
	clients Clients
}

// Start executes a synchronous controller or submits one asynchronous resource job.
func (handler nodeHandler) Start(ctx context.Context, command Command) (Result, error) {
	if command.NodeRun.InstanceKey != "" {
		return handler.submitResource(ctx, command)
	}

	switch command.NodeRun.Kind {
	case RequirementNode:
		requirement, err := handler.clients.Planner.AnalyzeRequirement(ctx, command.Input)
		if err != nil {
			return Result{}, err
		}
		return succeededArtifact(command.NodeRun, "requirement", requirement, nil)
	case StoryboardNode:
		requirement, err := artifactData[Requirement](command.Inputs, "requirement")
		if err != nil {
			return Result{}, err
		}
		storyboard, err := handler.clients.Planner.CreateStoryboard(ctx, requirement)
		if err != nil {
			return Result{}, err
		}
		return succeededArtifact(command.NodeRun, "storyboard", storyboard, nil)
	case CompetitionReferenceNode, PromptTTSNode, CharacterReferenceNode:
		return handler.planResources(ctx, command)
	default:
		return Result{}, fmt.Errorf("unsupported node kind: %s", command.NodeRun.Kind)
	}
}

// Refresh queries an existing asynchronous job; it is shared by callbacks and polling.
func (handler nodeHandler) Refresh(ctx context.Context, command Command) (Result, error) {
	if command.NodeRun.InstanceKey == "" {
		return Result{}, fmt.Errorf("controller node cannot be refreshed: %s", command.NodeRun.NodeID)
	}
	plan, err := decode[ResourcePlan](command.NodeRun.Output)
	if err != nil {
		return Result{}, err
	}
	if command.NodeRun.Kind == PromptTTSNode {
		return handler.refreshTTS(ctx, command, plan)
	}
	return handler.refreshImage(ctx, command, plan)
}

func (handler nodeHandler) planResources(ctx context.Context, command Command) (Result, error) {
	storyboard, err := artifactData[Storyboard](command.Inputs, "storyboard")
	if err != nil {
		return Result{}, err
	}

	var plans []ResourcePlan
	switch command.NodeRun.Kind {
	case CompetitionReferenceNode:
		plans, err = handler.clients.Planner.PlanCompetition(ctx, storyboard, command.Input)
	case PromptTTSNode:
		plans, err = handler.clients.Planner.PlanTTS(ctx, storyboard)
	case CharacterReferenceNode:
		plans, err = handler.clients.Planner.PlanCharacterReferences(ctx, storyboard, command.Input)
	}
	if err != nil {
		return Result{}, err
	}

	storyboardArtifact, err := findArtifact(command.Inputs, "storyboard")
	if err != nil {
		return Result{}, err
	}
	children := make([]NodeRun, 0, len(plans))
	for index, plan := range plans {
		if plan.ID == "" {
			plan.ID = fmt.Sprintf("item-%d", index+1)
		}
		if plan.ParentArtifactID == "" {
			plan.ParentArtifactID = storyboardArtifact.ID
		}
		output, err := encode(plan)
		if err != nil {
			return Result{}, err
		}
		children = append(children, NodeRun{
			NodeID:      command.NodeRun.NodeID,
			Kind:        command.NodeRun.Kind,
			InstanceKey: plan.ID,
			State:       Pending,
			SubmitKey:   newSubmitKey(command.RunID, command.NodeRun.NodeID, plan.ID),
			Output:      output,
		})
	}
	output, err := encode(plans)
	if err != nil {
		return Result{}, err
	}
	state := Running
	if len(children) == 0 {
		state = Succeeded
	}
	return Result{State: state, Output: output, Children: children}, nil
}

func (handler nodeHandler) submitResource(ctx context.Context, command Command) (Result, error) {
	plan, err := decode[ResourcePlan](command.NodeRun.Output)
	if err != nil {
		return Result{}, err
	}
	if command.NodeRun.Kind == PromptTTSNode {
		return handler.submitTTS(ctx, command, plan)
	}
	if command.NodeRun.Kind == CharacterReferenceNode {
		if err := handler.clients.Shield.CheckPrompt(ctx, plan.Prompt); err != nil {
			return failedResource(command.NodeRun, plan, err), nil
		}
	}
	return handler.submitImage(ctx, command, plan, false)
}

func (handler nodeHandler) submitTTS(ctx context.Context, command Command, plan ResourcePlan) (Result, error) {
	job, err := handler.clients.TTS.SubmitTTS(ctx, TTSRequest{
		Speaker:     plan.Speaker,
		Text:        plan.Text,
		WithExample: true,
		SubmitKey:   command.NodeRun.SubmitKey,
	})
	if err == nil {
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID, false), nil
	}
	job, found, lookupErr := handler.clients.TTS.FindTTSBySubmitKey(ctx, command.NodeRun.SubmitKey)
	if lookupErr != nil {
		return Result{}, lookupErr
	}
	if found {
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID, false), nil
	}
	return waitingResource(command.NodeRun, plan, "tts", "", false), nil
}

func (handler nodeHandler) submitImage(ctx context.Context, command Command, plan ResourcePlan, fallback bool) (Result, error) {
	submitKey := command.NodeRun.SubmitKey
	model := plan.Model
	if fallback {
		submitKey += ":fallback"
		model = plan.FallbackModel
	}
	job, err := handler.clients.Image.SubmitImage(ctx, ImageRequest{
		Prompt:    plan.Prompt,
		ImageURLs: plan.ImageURLs,
		Model:     model,
		SubmitKey: submitKey,
	})
	if err == nil {
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID, fallback), nil
	}
	job, found, lookupErr := handler.clients.Image.FindImageBySubmitKey(ctx, submitKey)
	if lookupErr != nil {
		return Result{}, lookupErr
	}
	if found {
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID, fallback), nil
	}
	return waitingResource(command.NodeRun, plan, "image", "", fallback), nil
}

func (handler nodeHandler) refreshTTS(ctx context.Context, command Command, plan ResourcePlan) (Result, error) {
	job, found, err := handler.findTTS(ctx, command.NodeRun)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return waitingResource(command.NodeRun, plan, "tts", "", false), nil
	}
	status, err := handler.clients.TTS.GetTTS(ctx, job.JobID)
	if err != nil {
		return Result{}, err
	}
	if status.State == JobPending {
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID, false), nil
	}
	if status.State == JobFailed {
		return failedResource(command.NodeRun, plan, fmt.Errorf("tts job failed: %s", status.Message)), nil
	}
	if status.State != JobSucceeded {
		return Result{}, fmt.Errorf("unknown tts job state: %s", status.State)
	}

	previewURI, previewURL := status.ExampleURI, status.ExampleURL
	if previewURI == "" {
		previewURI, previewURL = status.URI, status.URL
	}
	data, err := encode(map[string]string{
		"audio_uri":         status.URI,
		"audio_url":         status.URL,
		"example_audio_uri": status.ExampleURI,
		"example_audio_url": status.ExampleURL,
		"preview_audio_uri": previewURI,
		"preview_audio_url": previewURL,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{State: Succeeded, Provider: job.Provider, JobID: job.JobID, Artifacts: []Artifact{{
		ID:        artifactID(command.NodeRun),
		Kind:      "voice_preview",
		Status:    string(Succeeded),
		ParentIDs: parentIDs(plan),
		Data:      data,
	}}}, nil
}

func (handler nodeHandler) refreshImage(ctx context.Context, command Command, plan ResourcePlan) (Result, error) {
	job, found, err := handler.findImage(ctx, command.NodeRun)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return waitingResource(command.NodeRun, plan, "image", "", command.NodeRun.FallbackSubmitted), nil
	}
	status, err := handler.clients.Image.GetImage(ctx, job.JobID)
	if err != nil {
		return Result{}, err
	}
	if status.State == JobPending {
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID, command.NodeRun.FallbackSubmitted), nil
	}
	if status.State == JobFailed {
		return handler.fallbackOrFail(ctx, command, plan, fmt.Errorf("image job failed: %s", status.Message))
	}
	if status.State != JobSucceeded {
		return Result{}, fmt.Errorf("unknown image job state: %s", status.State)
	}

	imageRef := status.URL
	if imageRef == "" {
		imageRef = status.URI
	}
	if command.NodeRun.Kind == CharacterReferenceNode {
		if err := handler.clients.Audit.CheckImage(ctx, imageRef); err != nil {
			return handler.fallbackOrFail(ctx, command, plan, err)
		}
	}
	data, err := encode(map[string]string{"uri": status.URI, "url": status.URL})
	if err != nil {
		return Result{}, err
	}
	imageArtifact := Artifact{
		ID:        artifactID(command.NodeRun),
		Kind:      string(command.NodeRun.Kind),
		Status:    string(Succeeded),
		ParentIDs: parentIDs(plan),
		Data:      data,
	}
	artifacts := []Artifact{imageArtifact}
	if command.NodeRun.Kind == CompetitionReferenceNode {
		annotation, err := encode(map[string]string{
			"storyboard_artifact_id": plan.ParentArtifactID,
			"image_artifact_id":      imageArtifact.ID,
		})
		if err != nil {
			return Result{}, err
		}
		artifacts = append(artifacts, Artifact{
			ID:        imageArtifact.ID + ":annotation",
			Kind:      "storyboard_annotation",
			Status:    string(Succeeded),
			ParentIDs: []string{plan.ParentArtifactID, imageArtifact.ID},
			Data:      annotation,
		})
	}
	return Result{State: Succeeded, Provider: job.Provider, JobID: job.JobID, Artifacts: artifacts}, nil
}

func (handler nodeHandler) fallbackOrFail(ctx context.Context, command Command, plan ResourcePlan, cause error) (Result, error) {
	if command.NodeRun.Kind != CharacterReferenceNode || command.NodeRun.FallbackSubmitted || plan.FallbackModel == "" {
		return failedResource(command.NodeRun, plan, cause), nil
	}
	return handler.submitImage(ctx, command, plan, true)
}

func (handler nodeHandler) findTTS(ctx context.Context, node NodeRun) (SubmittedJob, bool, error) {
	if node.JobID != "" {
		return SubmittedJob{Provider: node.Provider, JobID: node.JobID}, true, nil
	}
	return handler.clients.TTS.FindTTSBySubmitKey(ctx, node.SubmitKey)
}

func (handler nodeHandler) findImage(ctx context.Context, node NodeRun) (SubmittedJob, bool, error) {
	if node.JobID != "" {
		return SubmittedJob{Provider: node.Provider, JobID: node.JobID}, true, nil
	}
	key := node.SubmitKey
	if node.FallbackSubmitted {
		key += ":fallback"
	}
	return handler.clients.Image.FindImageBySubmitKey(ctx, key)
}

func succeededArtifact(node NodeRun, kind string, value any, parents []string) (Result, error) {
	data, err := encode(value)
	if err != nil {
		return Result{}, err
	}
	return Result{State: Succeeded, Output: data, Artifacts: []Artifact{{
		ID:        artifactID(node),
		Kind:      kind,
		Status:    string(Succeeded),
		ParentIDs: parents,
		Data:      data,
	}}}, nil
}

func waitingResource(node NodeRun, plan ResourcePlan, provider, jobID string, fallback bool) Result {
	return Result{State: Waiting, Provider: provider, JobID: jobID, ClearJobID: fallback, FallbackSubmitted: fallback, Artifacts: []Artifact{{
		ID:        artifactID(node),
		Kind:      string(node.Kind),
		Status:    string(Waiting),
		ParentIDs: parentIDs(plan),
	}}}
}

func failedResource(node NodeRun, plan ResourcePlan, cause error) Result {
	return Result{State: Failed, Artifacts: []Artifact{{
		ID:        artifactID(node),
		Kind:      string(node.Kind),
		Status:    string(Failed),
		ParentIDs: parentIDs(plan),
		Message:   cause.Error(),
	}}, Message: cause.Error()}
}

func parentIDs(plan ResourcePlan) []string {
	if plan.ParentArtifactID == "" {
		return nil
	}
	return []string{plan.ParentArtifactID}
}

func findArtifact(artifacts []Artifact, kind string) (Artifact, error) {
	for _, artifact := range artifacts {
		if artifact.Kind == kind && artifact.Status == string(Succeeded) {
			return artifact, nil
		}
	}
	return Artifact{}, fmt.Errorf("artifact not found: %s", kind)
}

func artifactData[T any](artifacts []Artifact, kind string) (T, error) {
	artifact, err := findArtifact(artifacts, kind)
	if err != nil {
		var empty T
		return empty, err
	}
	return decode[T](artifact.Data)
}

func encode(value any) (json.RawMessage, error) {
	return json.Marshal(value)
}

func decode[T any](data json.RawMessage) (T, error) {
	var value T
	if len(data) == 0 {
		return value, fmt.Errorf("empty json data")
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, err
	}
	return value, nil
}
