//go:build bytedance

package videoagent

import (
	"context"
	"fmt"

	"code.byted.org/overpass/ad_site_creative_meta_server/kitex_gen/meta_common"
	"code.byted.org/overpass/ad_site_creative_meta_server/kitex_gen/meta_server"
	"code.byted.org/overpass/ad_site_creative_meta_server/kitex_gen/meta_v2"
	"code.byted.org/overpass/ad_site_creative_meta_server/rpc/ad_site_creative_meta_server"
)

type bytedanceVideoRenderer struct {
	bizID int
}

func NewBytedanceVideoRenderer(bizID int) (VideoRenderer, error) {
	if bizID <= 0 {
		return nil, fmt.Errorf("meta biz_id must be positive")
	}
	return bytedanceVideoRenderer{bizID: bizID}, nil
}

func (renderer bytedanceVideoRenderer) StartRender(ctx context.Context, plan RenderPlan) (string, error) {
	request := &meta_v2.RenderVideoBySchemaV2Request{
		TargetInfo: &meta_v2.TargetInfo{Width: int32(plan.Width), Height: int32(plan.Height)},
		MainTrack:  &meta_v2.Track{ZIndex: 0, Elements: make([]*meta_v2.Element, 0, len(plan.Scenes))},
		AuthInfo:   &meta_common.AuthInfo{BizId: meta_common.BizId(renderer.bizID)},
	}
	startTime := 0
	for _, scene := range plan.Scenes {
		duration := normalizedDurationMS(scene.DurationMS)
		clipStart, clipEnd := int32(scene.ClipStartMS), int32(scene.ClipEndMS)
		if clipEnd <= clipStart {
			clipEnd = clipStart + int32(duration)
		}
		start, end := int32(startTime), int32(startTime+duration)
		speed, volume := scene.Speed, float64(0)
		if speed <= 0 {
			speed = 1
		}
		elementID := scene.ID
		request.MainTrack.Elements = append(request.MainTrack.Elements, &meta_v2.Element{
			ElementType: meta_v2.ElementType_Video,
			ElementId:   &elementID,
			Video: &meta_v2.Video{
				Source: scene.Source, ClipStartTime: &clipStart, ClipEndTime: &clipEnd,
				StartTime: &start, EndTime: &end, Speed: &speed, Volume: &volume,
			},
		})
		startTime += duration
	}
	if len(plan.Audios) > 0 {
		request.AudioTrack = &meta_v2.AudioTrack{Audios: make([]*meta_v2.Audio, 0, len(plan.Audios))}
		for _, audio := range plan.Audios {
			end := audio.StartMS + audio.DurationMS
			start, clipStart, clipEnd := int32(audio.StartMS), int32(0), int32(audio.DurationMS)
			volume := float64(1)
			request.AudioTrack.Audios = append(request.AudioTrack.Audios, &meta_v2.Audio{
				Source: audio.Source, StartTime: start, EndTime: int32(end),
				ClipStartTime: &clipStart, ClipEndTime: &clipEnd, Volume: &volume,
			})
		}
	}
	response, err := ad_site_creative_meta_server.RawCall.RenderVideoBySchemaV2(ctx, request)
	if err != nil {
		return "", err
	}
	if response == nil || response.GetTaskId() == "" {
		return "", fmt.Errorf("meta renderer returned an empty task id")
	}
	return response.GetTaskId(), nil
}

func (bytedanceVideoRenderer) GetRender(ctx context.Context, taskID string) (JobStatus, error) {
	response, err := ad_site_creative_meta_server.RawCall.CheckRenderStatus(ctx, &meta_server.CheckRenderStatusRequest{TaskId: taskID})
	if err != nil {
		return JobStatus{}, err
	}
	if response == nil {
		return JobStatus{}, fmt.Errorf("meta renderer returned an empty status")
	}
	switch response.GetRenderStatus() {
	case meta_server.RenderStatus_Rendering:
		return JobStatus{State: JobPending}, nil
	case meta_server.RenderStatus_RenderSuccess:
		if response.GetVid() == "" {
			return JobStatus{}, fmt.Errorf("meta render succeeded without vid")
		}
		return JobStatus{State: JobSucceeded, URI: "vid://" + response.GetVid(), URL: response.GetVideoInfo().GetPlayUrl()}, nil
	case meta_server.RenderStatus_RenderFailed:
		return JobStatus{State: JobFailed, Message: "meta render failed"}, nil
	default:
		return JobStatus{}, fmt.Errorf("unknown meta render status: %s", response.GetRenderStatus())
	}
}

var _ VideoRenderer = bytedanceVideoRenderer{}
