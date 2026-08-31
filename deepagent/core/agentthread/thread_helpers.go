package agentthread

import (
	"context"

	"eino-cli/deepagent/core/graph"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

func copyConsumedInputsMeta(in []any) []any {
	if len(in) == 0 {
		return nil
	}
	hasValue := false
	for _, meta := range in {
		if meta != nil {
			hasValue = true
			break
		}
	}
	if !hasValue {
		return nil
	}
	out := make([]any, len(in))
	copy(out, in)
	return out
}

func defaultTurnIDProvider(context.Context, string, *Message) string {
	return uuid.NewString()
}

func copyResumeTurnOptions(in *ResumeTurnOptions) *ResumeTurnOptions {
	if in == nil {
		return nil
	}
	out := *in
	if len(in.ResumeInterruptIDs) > 0 {
		out.ResumeInterruptIDs = append([]string(nil), in.ResumeInterruptIDs...)
	}
	if len(in.ResumeData) > 0 {
		out.ResumeData = make(map[string]any, len(in.ResumeData))
		for k, v := range in.ResumeData {
			out.ResumeData[k] = v
		}
	}
	if in.RunnerConfig != nil {
		out.RunnerConfig = in.RunnerConfig.Clone()
	}
	return &out
}

func resumeTurnConfigRequestFromOptions(in *ResumeTurnOptions) *ResumeTurnConfigRequest {
	if in == nil {
		return nil
	}
	out := &ResumeTurnConfigRequest{
		CheckpointID:        in.CheckpointID,
		WriteToCheckpointID: in.WriteToCheckpointID,
		ForceNewRun:         in.ForceNewRun,
		ConfigMeta:          in.ConfigMeta,
	}
	if len(in.ResumeInterruptIDs) > 0 {
		out.ResumeInterruptIDs = append([]string(nil), in.ResumeInterruptIDs...)
	}
	if len(in.ResumeData) > 0 {
		out.ResumeData = make(map[string]any, len(in.ResumeData))
		for k, v := range in.ResumeData {
			out.ResumeData[k] = v
		}
	}
	return out
}

func copyResumeTurnConfigRequest(in *ResumeTurnConfigRequest) *ResumeTurnConfigRequest {
	if in == nil {
		return nil
	}
	out := *in
	if len(in.ResumeInterruptIDs) > 0 {
		out.ResumeInterruptIDs = append([]string(nil), in.ResumeInterruptIDs...)
	}
	if len(in.ResumeData) > 0 {
		out.ResumeData = make(map[string]any, len(in.ResumeData))
		for k, v := range in.ResumeData {
			out.ResumeData[k] = v
		}
	}
	return &out
}

func buildSubmitInputOptions(opts []SubmitInputOption) SubmitInputOptions {
	var out SubmitInputOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&out)
		}
	}
	return out
}

func copyMessages(msgs []*schema.Message) []*schema.Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]*schema.Message, len(msgs))
	for i, msg := range msgs {
		out[i] = graph.CopyMessage(msg)
	}
	return out
}
