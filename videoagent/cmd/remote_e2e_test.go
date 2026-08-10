//go:build fornax && bytedance

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	app "eino-cli/videoagent/backend/application"
	"eino-cli/videoagent/backend/messaging"
	videomodel "eino-cli/videoagent/backend/model"
)

func TestRemoteVideoAgentEndToEnd(t *testing.T) {
	if os.Getenv("VIDEO_AGENT_REMOTE_E2E") != "1" {
		t.Skip("set VIDEO_AGENT_REMOTE_E2E=1 to run real provider acceptance")
	}

	remoteConfig := requiredEnv(t, "VIDEO_AGENT_E2E_REMOTE_CONFIG")
	modelConfig, promptConfig, credentials := remoteE2EModelConfig(t)
	mongoURI := requiredEnv(t, "VIDEO_AGENT_E2E_MONGO_URI")
	natsURL := requiredEnv(t, "VIDEO_AGENT_E2E_NATS_URL")

	timeout := 45 * time.Minute
	if configured := os.Getenv("VIDEO_AGENT_E2E_TIMEOUT"); configured != "" {
		parsed, err := time.ParseDuration(configured)
		if err != nil {
			t.Fatalf("parse VIDEO_AGENT_E2E_TIMEOUT: %v", err)
		}
		timeout = parsed
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	suffix := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	bus, err := messaging.NewNATSMessageBus(ctx, messaging.NATSConfig{
		URL:      natsURL,
		Stream:   "VIDEO_AGENT_E2E_" + suffix,
		Subject:  "video_agent.e2e." + suffix,
		Consumer: "video_agent_e2e_" + suffix,
	})
	if err != nil {
		t.Fatalf("create NATS message bus: %v", err)
	}
	defer bus.Close()

	application, err := newApplication(
		ctx,
		t.TempDir(),
		remoteConfig,
		modelConfig,
		promptConfig,
		mongoURI,
		"video_agent_e2e_"+suffix,
		"workflow_state",
		firstEnv("VIDEO_AGENT_E2E_CHAT_MODEL_KEY", defaultCanvasAgentModelKey),
		credentials,
	)
	if err != nil {
		t.Fatalf("create remote application: %v", err)
	}
	application.SetMessageQueue(bus, bus)
	defer application.Close()
	if err := application.Start(ctx); err != nil {
		t.Fatalf("start remote application: %v", err)
	}

	productName := firstEnv("VIDEO_AGENT_E2E_PRODUCT_NAME", "软底厚底休闲鞋")
	brief := firstEnv("VIDEO_AGENT_E2E_BRIEF", "请结合商品卖点生成一条短视频广告，并运行当前工作流")
	reply, err := application.Agent.Chat(ctx, app.AgentChatInput{
		ProjectID:      "demo",
		IdempotencyKey: "remote-e2e-" + suffix,
		Text:           brief,
		RunInput: app.RunInput{
			ProductName:      productName,
			ProductImageURLs: splitLines(os.Getenv("VIDEO_AGENT_E2E_PRODUCT_IMAGES")),
			Brief:            brief,
		},
	})
	if err != nil {
		t.Fatalf("run Canvas Agent: %v", err)
	}
	if reply.Operation == nil || reply.Operation.Type != app.OperationRun {
		t.Fatalf("Canvas Agent did not propose a run operation: %#v", reply.Operation)
	}

	_, startedRun, err := application.Runner.ConfirmOperation(ctx, reply.Operation.ID)
	if err != nil {
		t.Fatalf("confirm run operation: %v", err)
	}
	if startedRun == nil {
		t.Fatal("confirm run operation returned no run")
	}

	run := waitForRemoteRun(t, ctx, application.Store, startedRun.ID)
	validateRemoteArtifacts(t, run)
	t.Logf("remote E2E succeeded: run_id=%s artifacts=%d", run.ID, artifactCount(run))
}

func remoteE2EModelConfig(t *testing.T) (modelConfig, promptConfig string, credentials *videomodel.CredentialsConfig) {
	t.Helper()
	credentialsPath := strings.TrimSpace(os.Getenv("VIDEO_AGENT_E2E_CREDENTIALS_CONFIG"))
	if credentialsPath == "" {
		return requiredEnv(t, "VIDEO_AGENT_E2E_MODEL_CONFIG"), requiredEnv(t, "VIDEO_AGENT_E2E_PROMPT_CONFIG"), nil
	}
	loaded, err := readJSON[videomodel.CredentialsConfig](credentialsPath)
	if err != nil {
		t.Fatalf("read VIDEO_AGENT_E2E_CREDENTIALS_CONFIG: %v", err)
	}
	return "", "", &loaded
}

func waitForRemoteRun(t *testing.T, ctx context.Context, store *app.Store, runID string) app.Run {
	t.Helper()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		run, err := store.Get(ctx, runID)
		if err != nil {
			t.Fatalf("load remote run %s: %v", runID, err)
		}
		finished := len(run.NodeRuns) > 0
		for _, node := range run.NodeRuns {
			if node.State == app.Failed || node.State == app.Canceled {
				payload, _ := json.MarshalIndent(run, "", "  ")
				t.Fatalf("remote run failed at %s/%s: %s\n%s", node.NodeID, node.InstanceKey, node.Message, payload)
			}
			if node.State != app.Succeeded {
				finished = false
			}
		}
		if finished {
			return run
		}
		select {
		case <-ctx.Done():
			payload, _ := json.MarshalIndent(run, "", "  ")
			t.Fatalf("wait for remote run: %v\n%s", ctx.Err(), payload)
		case <-ticker.C:
		}
	}
}

func validateRemoteArtifacts(t *testing.T, run app.Run) {
	t.Helper()
	artifacts := make(map[string]app.Artifact)
	kinds := make(map[string]int)
	for _, node := range run.NodeRuns {
		for _, artifact := range node.Artifacts {
			artifacts[artifact.ID] = artifact
			kinds[artifact.Kind]++
			validateRemoteMedia(t, artifact)
		}
	}
	for _, kind := range []string{
		"requirement",
		"clipscript",
		"competition_reference_image",
		"voice_preview",
		"character_reference_image",
		"preview_video",
		"finalvideo",
	} {
		if kinds[kind] == 0 {
			t.Errorf("remote run has no %s artifact", kind)
		}
	}

	for _, artifact := range artifacts {
		for _, parentID := range artifact.ParentIDs {
			if _, exists := artifacts[parentID]; !exists {
				t.Errorf("artifact %s references missing parent %s", artifact.ID, parentID)
			}
		}
		if artifact.Kind != "finalvideo" {
			continue
		}
		parentKinds := make(map[string]bool)
		for _, parentID := range artifact.ParentIDs {
			parentKinds[artifacts[parentID].Kind] = true
		}
		if !parentKinds["clipscript"] || !parentKinds["preview_video"] {
			t.Errorf("finalvideo parents = %#v, want clipscript and preview_video", parentKinds)
		}
	}
}

func validateRemoteMedia(t *testing.T, artifact app.Artifact) {
	t.Helper()
	var data map[string]any
	if len(artifact.Data) == 0 || json.Unmarshal(artifact.Data, &data) != nil {
		return
	}
	field := ""
	switch artifact.Kind {
	case "competition_reference_image", "character_reference_image", "preview_video", "finalvideo":
		field = "url"
	case "voice_preview":
		field = "preview_audio_url"
	default:
		return
	}
	mediaURL, _ := data[field].(string)
	if !strings.HasPrefix(mediaURL, "https://") && !strings.HasPrefix(mediaURL, "http://") {
		t.Errorf("artifact %s has non-displayable %s: %q", artifact.ID, field, mediaURL)
	}
}

func requiredEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required", name)
	}
	return value
}

func firstEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func splitLines(value string) []string {
	result := make([]string, 0)
	for _, line := range strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == ',' }) {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}

func artifactCount(run app.Run) int {
	count := 0
	for _, node := range run.NodeRuns {
		count += len(node.Artifacts)
	}
	return count
}
