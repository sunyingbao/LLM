package clienttest

import (
	"context"
	"errors"
	"testing"
	"time"

	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/definition"
	runtimeclient "eino-cli/deepagent/runtime"
)

type Factory func(t *testing.T) (client runtimeclient.Client, cleanup func())

func Run(t *testing.T, factory Factory) {
	RunWithOptions(t, factory, Options{Runtime: runtimeclient.RuntimeLocal, Namespace: "contract", SubmitReturnsTurnID: true})
}

type Options struct {
	Runtime             runtimeclient.RuntimeKind
	Namespace           string
	SubmitReturnsTurnID bool
}

func RunWithOptions(t *testing.T, factory Factory, options Options) {
	t.Helper()
	client, cleanup := factory(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	created, err := client.CreateThread(ctx, runtimeclient.CreateThreadRequest{
		Runtime:    options.Runtime,
		Namespace:  options.Namespace,
		Definition: agentdefinition.Definition{Name: "assistant", Version: "v1"},
		Title:      "contract thread",
	})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	ref := created.Thread.Ref
	if ref.Runtime != options.Runtime || ref.ThreadID == "" {
		t.Fatalf("created ref = %+v", ref)
	}

	got, err := client.GetThread(ctx, ref)
	if err != nil || got.Title != "contract thread" {
		t.Fatalf("GetThread() thread=%+v error=%v", got, err)
	}
	listed, err := client.ListThreads(ctx, runtimeclient.ListThreadsQuery{Runtime: options.Runtime, Namespace: options.Namespace})
	if err != nil || len(listed.Threads) != 1 {
		t.Fatalf("ListThreads() result=%+v error=%v", listed, err)
	}
	child, err := client.CreateThread(ctx, runtimeclient.CreateThreadRequest{
		Runtime: options.Runtime, Namespace: options.Namespace, ParentRef: &ref,
		Definition: agentdefinition.Definition{Name: "assistant", Version: "v1"}, Title: "child",
	})
	if err != nil || child.Thread.Ref.ThreadID == ref.ThreadID {
		t.Fatalf("CreateThread(child) result=%+v error=%v", child, err)
	}

	subscription, err := client.SubscribeTimeline(ctx, runtimeclient.TimelineQuery{Ref: ref})
	if err != nil {
		t.Fatalf("SubscribeTimeline() error = %v", err)
	}
	t.Cleanup(func() { _ = subscription.Close() })

	for turn := 0; turn < 2; turn++ {
		result, submitErr := client.Submit(ctx, runtimeclient.SubmitRequest{Ref: ref, Input: textInput("hello")})
		if submitErr != nil || options.SubmitReturnsTurnID && result.TurnID == "" {
			t.Fatalf("Submit(%d) result=%+v error=%v", turn, result, submitErr)
		}
	}
	var timeline *runtimeclient.TimelineResult
	deadline := time.Now().Add(time.Second)
	for {
		timeline, err = client.ListTimeline(ctx, runtimeclient.TimelineQuery{Ref: ref})
		if err != nil {
			t.Fatalf("ListTimeline() error = %v", err)
		}
		if len(timeline.Events) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ListTimeline() result=%+v", timeline)
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case event := <-subscription.Events():
		if event.ThreadID != ref.ThreadID {
			t.Fatalf("live event thread = %q", event.ThreadID)
		}
	case <-time.After(time.Second):
		t.Fatal("live timeline event was not delivered")
	}

	blocked, err := client.Submit(ctx, runtimeclient.SubmitRequest{Ref: ref, Input: textInput("block")})
	if err != nil {
		t.Fatalf("Submit(block) error = %v", err)
	}
	blockedTurnID := blocked.TurnID
	if blockedTurnID == "" {
		blockedTimeline, listErr := client.ListTimeline(ctx, runtimeclient.TimelineQuery{Ref: ref})
		if listErr != nil {
			t.Fatalf("ListTimeline(blocked) error=%v", listErr)
		}
		for index := len(blockedTimeline.Events) - 1; index >= 0; index-- {
			if blockedTimeline.Events[index].TurnID != "" {
				blockedTurnID = blockedTimeline.Events[index].TurnID
				break
			}
		}
		if blockedTurnID == "" {
			t.Fatal("blocked turn id was not observable in timeline")
		}
	}
	deadline = time.Now().Add(time.Second)
	for {
		blockedThread, getErr := client.GetThread(ctx, ref)
		if getErr != nil {
			t.Fatalf("GetThread(blocked) error = %v", getErr)
		}
		if blockedThread.State == runtimeclient.ThreadStateBlocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("thread state = %q, want blocked", blockedThread.State)
		}
		time.Sleep(time.Millisecond)
	}
	resumed, err := client.Resume(ctx, runtimeclient.ResumeRequest{Ref: ref, Payload: protoinput.ResumeTurnPayload{
		TurnID: blockedTurnID, CheckpointID: "checkpoint-1", InterruptID: "interrupt-1",
		Approval: &protoinput.ApprovalDecision{Approved: true},
	}})
	if err != nil || resumed.TurnID != blockedTurnID {
		t.Fatalf("Resume() result=%+v error=%v", resumed, err)
	}

	stop, err := client.Stop(ctx, runtimeclient.StopRequest{Ref: ref})
	if err != nil || !stop.Stopped {
		t.Fatalf("Stop() result=%+v error=%v", stop, err)
	}
	wrongRuntime := runtimeclient.RuntimeRemote
	if options.Runtime == runtimeclient.RuntimeRemote {
		wrongRuntime = runtimeclient.RuntimeLocal
	}
	_, err = client.GetThread(ctx, runtimeclient.GlobalThreadRef{Runtime: wrongRuntime, ThreadID: ref.ThreadID})
	if !errors.Is(err, runtimeclient.ErrInvalidArgument) {
		t.Fatalf("wrong runtime error = %v", err)
	}
}

func textInput(text string) (message protoinput.UserMessage) {
	message = protoinput.UserMessage{Parts: []protoinput.MessagePart{{Type: protoinput.MessagePartTypeText, Text: text}}}
	return message
}
