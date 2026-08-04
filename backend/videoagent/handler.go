package videoagent

import (
	"context"
	"encoding/json"
	"fmt"
)

type nodeHandler struct {
	clients Clients
}

// Start either produces a synchronous artifact, creates child jobs, or submits one child job.
func (handler nodeHandler) Start(ctx context.Context, command Command) (Result, error) {
	if command.NodeRun.InstanceKey != "" {
		return handler.submit(ctx, command)
	}

	switch command.NodeRun.Kind {
	case RequirementNode:
		requirement, err := handler.clients.Planner.AnalyzeRequirement(ctx, command.Input)
		if err != nil {
			return Result{}, err
		}
		return succeededArtifact(command.NodeRun, "requirement", requirement, nil)
	case ClipScriptNode:
		requirement, err := artifactData[Requirement](command.Inputs, "requirement")
		if err != nil {
			return Result{}, err
		}
		clipScript, err := handler.clients.Planner.CreateClipScript(ctx, requirement)
		if err != nil {
			return Result{}, err
		}
		return succeededArtifact(command.NodeRun, "clipscript", clipScript, nil)
	case CompetitionReferenceNode, PromptTTSNode, CharacterReferenceNode:
		return handler.planResources(ctx, command)
	case PreviewNode, FinalVideoNode:
		return planVideo(command)
	default:
		return Result{}, fmt.Errorf("unsupported node kind: %s", command.NodeRun.Kind)
	}
}

// Refresh reads only existing asynchronous jobs. A refresh never starts a new job.
func (handler nodeHandler) Refresh(ctx context.Context, command Command) (Result, error) {
	if command.NodeRun.InstanceKey == "" {
		return Result{}, fmt.Errorf("controller node cannot be refreshed: %s", command.NodeRun.NodeID)
	}
	plan, err := decode[ResourcePlan](command.NodeRun.Output)
	if err != nil {
		return Result{}, err
	}

	switch command.NodeRun.Kind {
	case PromptTTSNode:
		return handler.refreshTTS(ctx, command, plan)
	case CompetitionReferenceNode, CharacterReferenceNode:
		return handler.refreshImage(ctx, command, plan)
	case PreviewNode, FinalVideoNode:
		return handler.refreshVideo(ctx, command, plan)
	default:
		return Result{}, fmt.Errorf("unsupported async node kind: %s", command.NodeRun.Kind)
	}
}

func (handler nodeHandler) planResources(ctx context.Context, command Command) (Result, error) {
	clipScript, err := artifactData[ClipScript](command.Inputs, "clipscript")
	if err != nil {
		return Result{}, err
	}

	var plans []ResourcePlan
	switch command.NodeRun.Kind {
	case CompetitionReferenceNode:
		plans, err = handler.clients.Planner.PlanCompetition(ctx, clipScript, command.Input)
	case PromptTTSNode:
		plans, err = handler.clients.Planner.PlanTTS(ctx, clipScript)
	case CharacterReferenceNode:
		plans, err = handler.clients.Planner.PlanCharacterReferences(ctx, clipScript, command.Input)
	}
	if err != nil {
		return Result{}, err
	}

	clipScriptArtifact, err := findArtifact(command.Inputs, "clipscript")
	if err != nil {
		return Result{}, err
	}
	children := make([]NodeRun, 0, len(plans))
	ids := make(map[string]struct{}, len(plans))
	for index, plan := range plans {
		if plan.ID == "" {
			plan.ID = fmt.Sprintf("item-%d", index+1)
		}
		if _, duplicated := ids[plan.ID]; duplicated {
			return Result{}, fmt.Errorf("duplicate resource plan: %s", plan.ID)
		}
		ids[plan.ID] = struct{}{}
		if plan.ParentArtifactID == "" {
			plan.ParentArtifactID = clipScriptArtifact.ID
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
	if len(children) == 0 {
		return Result{State: Succeeded}, nil
	}
	return Result{State: Running, Children: children}, nil
}

func planVideo(command Command) (Result, error) {
	parentKind := "clipscript"
	if command.NodeRun.Kind == FinalVideoNode {
		parentKind = "preview_video"
	}
	parent, err := findArtifact(command.Inputs, parentKind)
	if err != nil {
		return Result{}, err
	}
	plan := ResourcePlan{ID: string(command.NodeRun.Kind), ParentArtifactID: parent.ID}
	for _, artifact := range command.Inputs {
		if artifact.Status == string(Succeeded) {
			plan.ArtifactIDs = append(plan.ArtifactIDs, artifact.ID)
		}
	}
	output, err := encode(plan)
	if err != nil {
		return Result{}, err
	}
	return Result{State: Running, Children: []NodeRun{{
		NodeID:      command.NodeRun.NodeID,
		Kind:        command.NodeRun.Kind,
		InstanceKey: plan.ID,
		State:       Pending,
		SubmitKey:   newSubmitKey(command.RunID, command.NodeRun.NodeID, plan.ID),
		Output:      output,
	}}}, nil
}

func (handler nodeHandler) submit(ctx context.Context, command Command) (Result, error) {
	plan, err := decode[ResourcePlan](command.NodeRun.Output)
	if err != nil {
		return Result{}, err
	}

	switch command.NodeRun.Kind {
	case PromptTTSNode:
		return handler.submitTTS(ctx, command, plan)
	case CompetitionReferenceNode, CharacterReferenceNode:
		if command.NodeRun.Kind == CharacterReferenceNode && !command.NodeRun.FallbackSubmitted {
			if err := handler.clients.Shield.CheckPrompt(ctx, plan.Prompt); err != nil {
				return failedResource(command.NodeRun, plan, err), nil
			}
		}
		return handler.submitImage(ctx, command, plan)
	case PreviewNode, FinalVideoNode:
		return handler.submitVideo(ctx, command, plan)
	default:
		return Result{}, fmt.Errorf("unsupported async node kind: %s", command.NodeRun.Kind)
	}
}

func (handler nodeHandler) submitTTS(ctx context.Context, command Command, plan ResourcePlan) (Result, error) {
	job, err := handler.clients.TTS.SubmitTTS(ctx, TTSRequest{
		Speaker:     plan.Speaker,
		Text:        plan.Text,
		WithExample: true,
		SubmitKey:   command.NodeRun.SubmitKey,
	})
	if err == nil {
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID), nil
	}
	job, found, _ := handler.clients.TTS.FindTTSBySubmitKey(ctx, command.NodeRun.SubmitKey)
	if found {
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID), nil
	}
	return waitingResource(command.NodeRun, plan, "tts", ""), nil
}

func (handler nodeHandler) submitImage(ctx context.Context, command Command, plan ResourcePlan) (Result, error) {
	submitKey := command.NodeRun.SubmitKey
	model := plan.Model
	if command.NodeRun.FallbackSubmitted {
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
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID), nil
	}
	job, found, _ := handler.clients.Image.FindImageBySubmitKey(ctx, submitKey)
	if found {
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID), nil
	}
	return waitingResource(command.NodeRun, plan, "image", ""), nil
}

func (handler nodeHandler) submitVideo(ctx context.Context, command Command, plan ResourcePlan) (Result, error) {
	request := VideoRequest{Inputs: append([]Artifact(nil), command.Inputs...), SubmitKey: command.NodeRun.SubmitKey}
	if command.NodeRun.Kind == PreviewNode {
		job, err := handler.clients.Video.SubmitPreview(ctx, request)
		if err == nil {
			return waitingResource(command.NodeRun, plan, job.Provider, job.JobID), nil
		}
		job, found, _ := handler.clients.Video.FindPreviewBySubmitKey(ctx, request.SubmitKey)
		if found {
			return waitingResource(command.NodeRun, plan, job.Provider, job.JobID), nil
		}
	} else {
		job, err := handler.clients.Video.SubmitFinalVideo(ctx, request)
		if err == nil {
			return waitingResource(command.NodeRun, plan, job.Provider, job.JobID), nil
		}
		job, found, _ := handler.clients.Video.FindFinalVideoBySubmitKey(ctx, request.SubmitKey)
		if found {
			return waitingResource(command.NodeRun, plan, job.Provider, job.JobID), nil
		}
	}
	return waitingResource(command.NodeRun, plan, "video", ""), nil
}

func (handler nodeHandler) refreshTTS(ctx context.Context, command Command, plan ResourcePlan) (Result, error) {
	job, found, err := handler.findTTS(ctx, command.NodeRun)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return waitingResource(command.NodeRun, plan, "tts", ""), nil
	}
	status, err := handler.clients.TTS.GetTTS(ctx, job.JobID)
	if err != nil {
		return Result{}, err
	}
	if status.State == JobPending {
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID), nil
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
	result, err := succeededArtifact(command.NodeRun, "voice_preview", map[string]string{
		"audio_uri":         status.URI,
		"audio_url":         status.URL,
		"example_audio_uri": status.ExampleURI,
		"example_audio_url": status.ExampleURL,
		"preview_audio_uri": previewURI,
		"preview_audio_url": previewURL,
	}, parentIDs(plan))
	if err != nil {
		return Result{}, err
	}
	result.Provider, result.JobID = job.Provider, job.JobID
	return result, nil
}

func (handler nodeHandler) refreshImage(ctx context.Context, command Command, plan ResourcePlan) (Result, error) {
	job, found, err := handler.findImage(ctx, command.NodeRun)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return waitingResource(command.NodeRun, plan, "image", ""), nil
	}
	status, err := handler.clients.Image.GetImage(ctx, job.JobID)
	if err != nil {
		return Result{}, err
	}
	if status.State == JobPending {
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID), nil
	}
	if status.State == JobFailed {
		return handler.fallbackOrFail(command, plan, fmt.Errorf("image job failed: %s", status.Message))
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
			return handler.fallbackOrFail(command, plan, err)
		}
	}
	result, err := succeededArtifact(command.NodeRun, string(command.NodeRun.Kind), map[string]string{"uri": status.URI, "url": status.URL}, parentIDs(plan))
	if err != nil || command.NodeRun.Kind != CompetitionReferenceNode {
		result.Provider, result.JobID = job.Provider, job.JobID
		return result, err
	}
	annotation, err := encode(map[string]string{
		"clipscript_artifact_id": plan.ParentArtifactID,
		"image_artifact_id":      result.Artifacts[0].ID,
	})
	if err != nil {
		return Result{}, err
	}
	result.Artifacts = append(result.Artifacts, Artifact{
		ID:        result.Artifacts[0].ID + ":annotation",
		Kind:      "clipscript_annotation",
		Status:    string(Succeeded),
		ParentIDs: []string{plan.ParentArtifactID, result.Artifacts[0].ID},
		Data:      annotation,
	})
	result.Provider, result.JobID = job.Provider, job.JobID
	return result, nil
}

func (handler nodeHandler) refreshVideo(ctx context.Context, command Command, plan ResourcePlan) (Result, error) {
	job, found, err := handler.findVideo(ctx, command.NodeRun)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return waitingResource(command.NodeRun, plan, "video", ""), nil
	}

	var status JobStatus
	if command.NodeRun.Kind == PreviewNode {
		status, err = handler.clients.Video.GetPreview(ctx, job.JobID)
	} else {
		status, err = handler.clients.Video.GetFinalVideo(ctx, job.JobID)
	}
	if err != nil {
		return Result{}, err
	}
	if status.State == JobPending {
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID), nil
	}
	if status.State == JobFailed {
		return failedResource(command.NodeRun, plan, fmt.Errorf("video job failed: %s", status.Message)), nil
	}
	if status.State != JobSucceeded {
		return Result{}, fmt.Errorf("unknown video job state: %s", status.State)
	}

	kind := "preview_video"
	if command.NodeRun.Kind == FinalVideoNode {
		kind = "finalvideo"
	}
	result, err := succeededArtifact(command.NodeRun, kind, map[string]string{"uri": status.URI, "url": status.URL}, parentIDs(plan))
	if err != nil {
		return Result{}, err
	}
	result.Provider, result.JobID = job.Provider, job.JobID
	return result, nil
}

func (handler nodeHandler) fallbackOrFail(command Command, plan ResourcePlan, cause error) (Result, error) {
	if command.NodeRun.Kind != CharacterReferenceNode || command.NodeRun.FallbackSubmitted || plan.FallbackModel == "" {
		return failedResource(command.NodeRun, plan, cause), nil
	}
	return Result{State: Pending, ClearJobID: true, FallbackSubmitted: true, ResetSubmission: true}, nil
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

func (handler nodeHandler) findVideo(ctx context.Context, node NodeRun) (SubmittedJob, bool, error) {
	if node.JobID != "" {
		return SubmittedJob{Provider: node.Provider, JobID: node.JobID}, true, nil
	}
	if node.Kind == PreviewNode {
		return handler.clients.Video.FindPreviewBySubmitKey(ctx, node.SubmitKey)
	}
	return handler.clients.Video.FindFinalVideoBySubmitKey(ctx, node.SubmitKey)
}

func succeededArtifact(node NodeRun, kind string, value any, parents []string) (Result, error) {
	data, err := encode(value)
	if err != nil {
		return Result{}, err
	}
	return Result{State: Succeeded, Artifacts: []Artifact{{
		ID:        artifactID(node),
		Kind:      kind,
		Status:    string(Succeeded),
		ParentIDs: parents,
		Data:      data,
	}}}, nil
}

func waitingResource(node NodeRun, plan ResourcePlan, provider, jobID string) Result {
	return Result{State: Waiting, Provider: provider, JobID: jobID, Artifacts: []Artifact{{
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
	if len(plan.ArtifactIDs) > 0 {
		return append([]string(nil), plan.ArtifactIDs...)
	}
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
