package videoagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
)

type nodeHandler struct {
	clients Clients
	store   *Store
}

func (handler nodeHandler) Cancel(ctx context.Context, run Run) error {
	var cancelErrs []error
	for _, node := range run.NodeRuns {
		if node.JobID == "" || node.State.terminal() {
			continue
		}
		var err error
		switch node.Kind {
		case PromptTTSNode:
			if canceler, ok := handler.clients.TTS.(TTSCanceler); ok {
				err = canceler.CancelTTS(ctx, node.JobID)
			} else {
				err = ErrCancellationUnsupported
			}
		case CompetitionReferenceNode, CharacterReferenceNode:
			if canceler, ok := handler.imageClient(node.Kind).(ImageCanceler); ok {
				err = canceler.CancelImage(ctx, node.JobID)
			} else {
				err = ErrCancellationUnsupported
			}
		case PreviewNode, FinalVideoNode:
			if canceler, ok := handler.clients.Video.(VideoCanceler); ok {
				err = canceler.CancelVideo(ctx, node.JobID)
			} else {
				err = ErrCancellationUnsupported
			}
		}
		if err != nil {
			cancelErrs = append(cancelErrs, fmt.Errorf("cancel %s/%s: %w", node.NodeID, node.InstanceKey, err))
		}
	}
	return errors.Join(cancelErrs...)
}

// Start either produces a synchronous artifact, creates child jobs, or submits one child job.
func (handler nodeHandler) Start(ctx context.Context, command Command) (Result, error) {
	config, err := decodeNodeConfig(command.NodeRun.Config)
	if err != nil {
		return Result{}, err
	}
	command.Input.Brief = configuredBrief(command.Input.Brief, config.Instruction)
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
		clipScript, err := handler.clients.Planner.CreateClipScript(ctx, requirement, command.Input)
		if err != nil {
			return Result{}, err
		}
		return succeededArtifact(command.NodeRun, "clipscript", clipScript, nil)
	case CompetitionReferenceNode, PromptTTSNode, CharacterReferenceNode:
		return handler.planResources(ctx, command, config)
	case PreviewNode:
		return handler.planPreview(ctx, command, config)
	case FinalVideoNode:
		return planFinalVideo(command, config)
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

func (handler nodeHandler) planResources(ctx context.Context, command Command, config NodeConfig) (Result, error) {
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
		applyNodeConfig(&plan, config)
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
			Config:      append([]byte(nil), command.NodeRun.Config...),
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

func planPreview(command Command, configs ...NodeConfig) (Result, error) {
	config := firstNodeConfig(configs)
	clipScriptArtifact, err := findArtifact(command.Inputs, "clipscript")
	if err != nil {
		return Result{}, err
	}
	clipScript, err := decode[ClipScript](clipScriptArtifact.Data)
	if err != nil {
		return Result{}, err
	}
	applyVoiceDurations(&clipScript, command.Inputs)
	plans := make([]ResourcePlan, 0, len(clipScript.Scenes))
	for index, scene := range clipScript.Scenes {
		if scene.ID == "" {
			scene.ID = fmt.Sprintf("scene-%d", index+1)
		}
		plan := ResourcePlan{
			ID: scene.ID, SceneID: scene.ID, ParentArtifactID: clipScriptArtifact.ID,
			ArtifactIDs: matchingArtifactIDs(command.Inputs, scene.ID),
			Prompt:      scene.Visual, Duration: durationSeconds(scene.DurationMS),
		}
		applyNodeConfig(&plan, config)
		plans = append(plans, plan)
	}
	return previewChildren(command, clipScriptArtifact, plans)
}

func (handler nodeHandler) planPreview(ctx context.Context, command Command, config NodeConfig) (Result, error) {
	if handler.clients.PreviewPlanner == nil {
		return planPreview(command, config)
	}
	clipScriptArtifact, err := findArtifact(command.Inputs, "clipscript")
	if err != nil {
		return Result{}, err
	}
	clipScript, err := decode[ClipScript](clipScriptArtifact.Data)
	if err != nil {
		return Result{}, err
	}
	applyVoiceDurations(&clipScript, command.Inputs)
	plans, err := handler.clients.PreviewPlanner.PlanPreview(ctx, clipScript, command.Input, command.Inputs)
	if err != nil {
		return Result{}, err
	}
	for index := range plans {
		if plans[index].ParentArtifactID == "" {
			plans[index].ParentArtifactID = clipScriptArtifact.ID
		}
		if len(plans[index].ArtifactIDs) == 0 {
			plans[index].ArtifactIDs = matchingArtifactIDs(command.Inputs, plans[index].SceneID)
		}
		applyNodeConfig(&plans[index], config)
	}
	return previewChildren(command, clipScriptArtifact, plans)
}

func previewChildren(command Command, clipScriptArtifact Artifact, plans []ResourcePlan) (Result, error) {
	children := make([]NodeRun, 0, len(plans))
	for index, plan := range plans {
		if plan.ID == "" {
			plan.ID = fmt.Sprintf("candidate-%d", index+1)
		}
		output, err := encode(plan)
		if err != nil {
			return Result{}, err
		}
		children = append(children, NodeRun{
			NodeID: command.NodeRun.NodeID, Kind: command.NodeRun.Kind, InstanceKey: plan.ID,
			Config: append([]byte(nil), command.NodeRun.Config...),
			State:  Pending, SubmitKey: newSubmitKey(command.RunID, command.NodeRun.NodeID, plan.ID), Output: output,
		})
	}
	if len(children) == 0 {
		return Result{}, fmt.Errorf("preview planner returned no candidates for %s", clipScriptArtifact.ID)
	}
	return Result{State: Running, Children: children}, nil
}

func planFinalVideo(command Command, configs ...NodeConfig) (Result, error) {
	config := firstNodeConfig(configs)
	parent, err := findArtifact(command.Inputs, "clipscript")
	if err != nil {
		return Result{}, err
	}

	cuts := previewCutNumbers(command.Inputs)
	if len(cuts) == 0 {
		cuts = []int32{0}
	}
	children := make([]NodeRun, 0, len(cuts))
	for _, cut := range cuts {
		planID := string(command.NodeRun.Kind)
		if cut > 0 {
			planID = fmt.Sprintf("cut-%d", cut)
		}
		plan := ResourcePlan{
			ID:               planID,
			ParentArtifactID: parent.ID,
			ArtifactIDs:      finalVideoArtifactIDs(command.Inputs, cut),
		}
		if cut > 0 {
			plan.CutNumbers = []int32{cut}
		}
		applyNodeConfig(&plan, config)
		output, err := encode(plan)
		if err != nil {
			return Result{}, err
		}
		children = append(children, NodeRun{
			NodeID: command.NodeRun.NodeID, Kind: command.NodeRun.Kind, InstanceKey: plan.ID,
			Config: append([]byte(nil), command.NodeRun.Config...),
			State:  Pending, SubmitKey: newSubmitKey(command.RunID, command.NodeRun.NodeID, plan.ID), Output: output,
		})
	}
	return Result{State: Running, Children: children}, nil
}

func previewCutNumbers(artifacts []Artifact) []int32 {
	seen := make(map[int32]bool)
	for _, artifact := range artifacts {
		if artifact.Kind != "preview_video" || artifact.Status != string(Succeeded) {
			continue
		}
		for _, cut := range artifactInt32s(artifact, "cut_numbers") {
			if cut > 0 {
				seen[cut] = true
			}
		}
	}
	cuts := make([]int32, 0, len(seen))
	for cut := range seen {
		cuts = append(cuts, cut)
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i] < cuts[j] })
	return cuts
}

func finalVideoArtifactIDs(artifacts []Artifact, cut int32) []string {
	ids := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.Status != string(Succeeded) {
			continue
		}
		if artifact.Kind == "preview_video" && cut > 0 && !artifactBelongsToCut(artifact, cut) {
			continue
		}
		ids = append(ids, artifact.ID)
	}
	return ids
}

func artifactBelongsToCut(artifact Artifact, cut int32) bool {
	for _, number := range artifactInt32s(artifact, "cut_numbers") {
		if number == cut {
			return true
		}
	}
	return false
}

func matchingArtifactIDs(artifacts []Artifact, sceneID string) []string {
	ids := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.Status != string(Succeeded) {
			continue
		}
		artifactSceneID := firstArtifactValue(artifact, "scene_id")
		if artifactSceneID == "" || artifactSceneID == sceneID {
			ids = append(ids, artifact.ID)
		}
	}
	return ids
}

func selectArtifacts(artifacts []Artifact, ids []string) []Artifact {
	if len(ids) == 0 {
		return append([]Artifact(nil), artifacts...)
	}
	selected := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		selected[id] = struct{}{}
	}
	result := make([]Artifact, 0, len(ids))
	for _, artifact := range artifacts {
		if _, ok := selected[artifact.ID]; ok {
			result = append(result, artifact)
		}
	}
	return result
}

func durationSeconds(milliseconds int) int {
	if milliseconds <= 0 {
		return 5
	}
	return (milliseconds + 999) / 1000
}

func decodeNodeConfig(raw json.RawMessage) (NodeConfig, error) {
	if len(raw) == 0 {
		return NodeConfig{}, nil
	}
	config, err := decode[NodeConfig](raw)
	if err != nil {
		return NodeConfig{}, fmt.Errorf("decode node config: %w", err)
	}
	return config, nil
}

func firstNodeConfig(configs []NodeConfig) NodeConfig {
	if len(configs) == 0 {
		return NodeConfig{}
	}
	return configs[0]
}

func configuredBrief(brief, instruction string) string {
	if instruction == "" {
		return brief
	}
	if brief == "" {
		return instruction
	}
	return brief + "\n" + instruction
}

func applyNodeConfig(plan *ResourcePlan, config NodeConfig) {
	if config.Prompt != "" {
		plan.Prompt = config.Prompt
	}
	if config.Model != "" {
		plan.Model = config.Model
	}
	if config.Width > 0 {
		plan.Width = config.Width
	}
	if config.Height > 0 {
		plan.Height = config.Height
	}
	if config.Duration > 0 {
		plan.Duration = config.Duration
	}
	if config.AspectRatio != "" {
		plan.AspectRatio = config.AspectRatio
	}
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
		Prompt:      plan.Prompt,
		Speaker:     plan.Speaker,
		Text:        plan.Text,
		CPM:         plan.CPM,
		Async:       true,
		WithExample: true,
		SubmitKey:   command.NodeRun.SubmitKey,
	})
	if err == nil {
		if job.Status != nil {
			handler.rememberSubmission(ctx, command.NodeRun, command.NodeRun.SubmitKey, job)
			return handler.ttsResult(command, plan, job, *job.Status)
		}
		if job.JobID == "" {
			return unknownSubmissionFailure(command, plan, "tts", errors.New("tts submit returned an empty job id")), nil
		}
		handler.rememberSubmission(ctx, command.NodeRun, command.NodeRun.SubmitKey, job)
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID), nil
	}
	job, found, findErr := handler.clients.TTS.FindTTSBySubmitKey(ctx, command.NodeRun.SubmitKey)
	if errors.Is(findErr, ErrSubmitReconciliationUnsupported) {
		return unknownSubmissionFailure(command, plan, "tts", err), nil
	}
	if found {
		if job.Status != nil {
			handler.rememberSubmission(ctx, command.NodeRun, command.NodeRun.SubmitKey, job)
			return handler.ttsResult(command, plan, job, *job.Status)
		}
		if job.JobID == "" {
			return unknownSubmissionFailure(command, plan, "tts", errors.New("tts reconciliation returned an empty job id")), nil
		}
		handler.rememberSubmission(ctx, command.NodeRun, command.NodeRun.SubmitKey, job)
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID), nil
	}
	return unknownSubmission(command.NodeRun, plan, "tts", err, findErr), nil
}

func (handler nodeHandler) submitImage(ctx context.Context, command Command, plan ResourcePlan) (Result, error) {
	client := handler.imageClient(command.NodeRun.Kind)
	submitKey := command.NodeRun.SubmitKey
	model := plan.Model
	if command.NodeRun.FallbackSubmitted {
		submitKey += ":fallback"
		model = plan.FallbackModel
	}
	job, err := client.SubmitImage(ctx, ImageRequest{
		Prompt:    plan.Prompt,
		ImageURLs: plan.ImageURLs,
		Model:     model,
		Width:     plan.Width,
		Height:    plan.Height,
		SubmitKey: submitKey,
	})
	if err == nil {
		if job.Status != nil {
			handler.rememberSubmission(ctx, command.NodeRun, submitKey, job)
			return handler.imageResult(ctx, command, plan, job, *job.Status)
		}
		if job.JobID == "" {
			return unknownSubmissionFailure(command, plan, "image", errors.New("image submit returned an empty job id")), nil
		}
		handler.rememberSubmission(ctx, command.NodeRun, submitKey, job)
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID), nil
	}
	job, found, findErr := client.FindImageBySubmitKey(ctx, submitKey)
	if errors.Is(findErr, ErrSubmitReconciliationUnsupported) {
		return unknownSubmissionFailure(command, plan, "image", err), nil
	}
	if found {
		if job.Status != nil {
			handler.rememberSubmission(ctx, command.NodeRun, submitKey, job)
			return handler.imageResult(ctx, command, plan, job, *job.Status)
		}
		if job.JobID == "" {
			return unknownSubmissionFailure(command, plan, "image", errors.New("image reconciliation returned an empty job id")), nil
		}
		handler.rememberSubmission(ctx, command.NodeRun, submitKey, job)
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID), nil
	}
	return unknownSubmission(command.NodeRun, plan, "image", err, findErr), nil
}

func (handler nodeHandler) submitVideo(ctx context.Context, command Command, plan ResourcePlan) (Result, error) {
	if command.NodeRun.Kind == PreviewNode && plan.ExistingVideoURI != "" {
		return handler.videoResult(command, plan, SubmittedJob{Provider: "material", JobID: plan.ID}, JobStatus{
			State: JobSucceeded,
			URI:   plan.ExistingVideoURI,
		})
	}
	inputs := selectArtifacts(command.Inputs, plan.ArtifactIDs)
	clipScript, err := optionalArtifactData[ClipScript](inputs, "clipscript")
	if err != nil {
		return Result{}, err
	}
	request := VideoRequest{
		Inputs:               inputs,
		ClipScript:           clipScript,
		Prompt:               plan.Prompt,
		Duration:             plan.Duration,
		Model:                plan.Model,
		Width:                plan.Width,
		Height:               plan.Height,
		AspectRatio:          plan.AspectRatio,
		ImageURLs:            append([]string(nil), plan.ImageURLs...),
		AudioURLs:            append([]string(nil), plan.AudioURLs...),
		VideoURLs:            append([]string(nil), plan.VideoURLs...),
		Strategy:             plan.Strategy,
		OriginPictureClipIDs: append([]int64(nil), plan.OriginPictureClipIDs...),
		CutNumbers:           append([]int32(nil), plan.CutNumbers...),
		SubmitKey:            command.NodeRun.SubmitKey,
	}
	submit := handler.clients.Video.SubmitPreview
	find := handler.clients.Video.FindPreviewBySubmitKey
	name := "preview"
	if command.NodeRun.Kind == FinalVideoNode {
		name = "finalvideo"
		submit = handler.clients.Video.SubmitFinalVideo
		find = handler.clients.Video.FindFinalVideoBySubmitKey
	}
	job, err := submit(ctx, request)
	if err == nil {
		if job.Status != nil {
			handler.rememberSubmission(ctx, command.NodeRun, request.SubmitKey, job)
			return handler.videoResult(command, plan, job, *job.Status)
		}
		if job.JobID == "" {
			return unknownSubmissionFailure(command, plan, name, fmt.Errorf("%s submit returned an empty job id", name)), nil
		}
		handler.rememberSubmission(ctx, command.NodeRun, request.SubmitKey, job)
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID), nil
	}
	job, found, findErr := find(ctx, request.SubmitKey)
	if errors.Is(findErr, ErrSubmitReconciliationUnsupported) {
		return unknownSubmissionFailure(command, plan, name, err), nil
	}
	if found {
		if job.Status != nil {
			handler.rememberSubmission(ctx, command.NodeRun, request.SubmitKey, job)
			return handler.videoResult(command, plan, job, *job.Status)
		}
		if job.JobID == "" {
			return unknownSubmissionFailure(command, plan, name, fmt.Errorf("%s reconciliation returned an empty job id", name)), nil
		}
		handler.rememberSubmission(ctx, command.NodeRun, request.SubmitKey, job)
		return waitingResource(command.NodeRun, plan, job.Provider, job.JobID), nil
	}
	return unknownSubmission(command.NodeRun, plan, "video", err, findErr), nil
}

func (handler nodeHandler) refreshTTS(ctx context.Context, command Command, plan ResourcePlan) (Result, error) {
	job, found, err := handler.findTTS(ctx, command.NodeRun)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return waitingResource(command.NodeRun, plan, "tts", ""), nil
	}
	if job.Status != nil {
		return handler.ttsResult(command, plan, job, *job.Status)
	}
	status, err := handler.clients.TTS.GetTTS(ctx, job.JobID)
	if err != nil {
		return Result{}, err
	}
	return handler.ttsResult(command, plan, job, status)
}

func (handler nodeHandler) ttsResult(command Command, plan ResourcePlan, job SubmittedJob, status JobStatus) (Result, error) {
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
		previewURI = status.URI
	}
	if previewURL == "" {
		previewURL = status.URL
	}
	result, err := succeededArtifact(command.NodeRun, "voice_preview", map[string]any{
		"scene_id":          plan.SceneID,
		"audio_uri":         status.URI,
		"audio_url":         status.URL,
		"example_audio_uri": status.ExampleURI,
		"example_audio_url": status.ExampleURL,
		"preview_audio_uri": previewURI,
		"preview_audio_url": previewURL,
		"duration_ms":       status.DurationMS,
	}, parentIDs(plan))
	if err != nil {
		return Result{}, err
	}
	result.Provider, result.JobID = job.Provider, job.JobID
	return result, nil
}

func applyVoiceDurations(script *ClipScript, artifacts []Artifact) {
	if script == nil {
		return
	}
	voices := artifactsByScene(artifacts, "voice_preview")
	for index := range script.Scenes {
		voice, ok := voices[script.Scenes[index].ID]
		if !ok {
			continue
		}
		if duration := firstArtifactInt(voice, "duration_ms"); duration > script.Scenes[index].DurationMS {
			script.Scenes[index].DurationMS = duration
		}
	}
}

func (handler nodeHandler) refreshImage(ctx context.Context, command Command, plan ResourcePlan) (Result, error) {
	job, found, err := handler.findImage(ctx, command.NodeRun)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return waitingResource(command.NodeRun, plan, "image", ""), nil
	}
	if job.Status != nil {
		return handler.imageResult(ctx, command, plan, job, *job.Status)
	}
	status, err := handler.imageClient(command.NodeRun.Kind).GetImage(ctx, job.JobID)
	if err != nil {
		return Result{}, err
	}
	return handler.imageResult(ctx, command, plan, job, status)
}

func (handler nodeHandler) imageResult(ctx context.Context, command Command, plan ResourcePlan, job SubmittedJob, status JobStatus) (Result, error) {
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
	result, err := succeededArtifact(command.NodeRun, string(command.NodeRun.Kind), map[string]string{
		"scene_id": plan.SceneID,
		"uri":      status.URI,
		"url":      status.URL,
	}, parentIDs(plan))
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
	if job.Status != nil {
		return handler.videoResult(command, plan, job, *job.Status)
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
	return handler.videoResult(command, plan, job, status)
}

func (handler nodeHandler) videoResult(command Command, plan ResourcePlan, job SubmittedJob, status JobStatus) (Result, error) {
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
	data := map[string]any{
		"scene_id":                plan.SceneID,
		"candidate_id":            plan.ID,
		"uri":                     status.URI,
		"url":                     status.URL,
		"duration_seconds":        plan.Duration,
		"origin_picture_clip_ids": plan.OriginPictureClipIDs,
		"cut_numbers":             plan.CutNumbers,
		"cut_placements":          plan.CutPlacements,
		"strategy":                plan.Strategy,
		"clip_start_ms":           plan.ClipStartMS,
		"clip_end_ms":             plan.ClipEndMS,
	}
	result, err := succeededArtifact(command.NodeRun, kind, data, parentIDs(plan))
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
	if job, found, err := handler.storedSubmission(ctx, node.SubmitKey); found || err != nil {
		return job, found, err
	}
	job, found, err := handler.clients.TTS.FindTTSBySubmitKey(ctx, node.SubmitKey)
	if err == nil && found {
		handler.rememberSubmission(ctx, node, node.SubmitKey, job)
	}
	return job, found, err
}

func (handler nodeHandler) findImage(ctx context.Context, node NodeRun) (SubmittedJob, bool, error) {
	if node.JobID != "" {
		return SubmittedJob{Provider: node.Provider, JobID: node.JobID}, true, nil
	}
	key := node.SubmitKey
	if node.FallbackSubmitted {
		key += ":fallback"
	}
	if job, found, err := handler.storedSubmission(ctx, key); found || err != nil {
		return job, found, err
	}
	job, found, err := handler.imageClient(node.Kind).FindImageBySubmitKey(ctx, key)
	if err == nil && found {
		handler.rememberSubmission(ctx, node, key, job)
	}
	return job, found, err
}

func (handler nodeHandler) imageClient(kind NodeKind) ImageClient {
	if kind == CharacterReferenceNode && handler.clients.CharacterImage != nil {
		return handler.clients.CharacterImage
	}
	return handler.clients.Image
}

func (handler nodeHandler) findVideo(ctx context.Context, node NodeRun) (SubmittedJob, bool, error) {
	if node.JobID != "" {
		return SubmittedJob{Provider: node.Provider, JobID: node.JobID}, true, nil
	}
	if job, found, err := handler.storedSubmission(ctx, node.SubmitKey); found || err != nil {
		return job, found, err
	}
	var job SubmittedJob
	var found bool
	var err error
	if node.Kind == PreviewNode {
		job, found, err = handler.clients.Video.FindPreviewBySubmitKey(ctx, node.SubmitKey)
	} else {
		job, found, err = handler.clients.Video.FindFinalVideoBySubmitKey(ctx, node.SubmitKey)
	}
	if err == nil && found {
		handler.rememberSubmission(ctx, node, node.SubmitKey, job)
	}
	return job, found, err
}

func (handler nodeHandler) rememberSubmission(ctx context.Context, node NodeRun, submitKey string, job SubmittedJob) {
	if handler.store == nil {
		return
	}
	if err := handler.store.SaveSubmission(ctx, submitKey, job); err != nil {
		log.Printf("[nodeHandler] persist submission receipt failed node_id=%s instance_key=%s submit_key=%s provider=%s job_id=%s err=%v",
			node.NodeID, node.InstanceKey, submitKey, job.Provider, job.JobID, err)
	}
}

func (handler nodeHandler) storedSubmission(ctx context.Context, submitKey string) (SubmittedJob, bool, error) {
	if handler.store == nil {
		return SubmittedJob{}, false, nil
	}
	return handler.store.GetSubmission(ctx, submitKey)
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
	return waitingResourceWithMessage(node, plan, provider, jobID, "")
}

func waitingResourceWithMessage(node NodeRun, plan ResourcePlan, provider, jobID, message string) Result {
	return Result{State: Waiting, Provider: provider, JobID: jobID, Artifacts: []Artifact{{
		ID:        artifactID(node),
		Kind:      string(node.Kind),
		Status:    string(Waiting),
		ParentIDs: parentIDs(plan),
	}}, Message: message}
}

func unknownSubmission(node NodeRun, plan ResourcePlan, provider string, submitErr, findErr error) Result {
	message := fmt.Sprintf("%s submit outcome is unknown; waiting for submit-key reconciliation", provider)
	if findErr != nil {
		message = fmt.Sprintf("%s submit outcome is unknown; submit=%v; reconcile=%v", provider, submitErr, findErr)
	}
	return waitingResourceWithMessage(node, plan, provider, "", message)
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

func unknownSubmissionFailure(command Command, plan ResourcePlan, provider string, cause error) Result {
	log.Printf("[nodeHandler] submission outcome unknown; automatic retry disabled run_id=%s node_id=%s instance_key=%s submit_key=%s provider=%s err=%v",
		command.RunID, command.NodeRun.NodeID, command.NodeRun.InstanceKey, command.NodeRun.SubmitKey, provider, cause)
	result := failedResource(command.NodeRun, plan, fmt.Errorf("%s submit failed and cannot be reconciled safely: %w", provider, cause))
	result.Provider = provider
	result.SubmissionUnknown = true
	return result
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

func optionalArtifactData[T any](artifacts []Artifact, kind string) (*T, error) {
	for _, artifact := range artifacts {
		if artifact.Kind != kind || artifact.Status != string(Succeeded) {
			continue
		}
		value, err := decode[T](artifact.Data)
		if err != nil {
			return nil, err
		}
		return &value, nil
	}
	return nil, nil
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
