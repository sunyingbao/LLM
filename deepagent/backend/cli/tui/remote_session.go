package tui

import (
	"context"
	"fmt"
	"strings"

	"eino-cli/deepagent/cloud/protocol/timeline"
	clientruntime "eino-cli/deepagent/host/runtime"
	tea "github.com/charmbracelet/bubbletea"
)

type remoteSessionRuntime interface {
	clientruntime.InteractiveRuntime
	OpenSession(ctx context.Context, sessionID string) (err error)
	ListSessions(ctx context.Context) (sessions []clientruntime.SessionInfo, err error)
	Session() (session clientruntime.SessionInfo)
	FollowSession(ctx context.Context) (history []timeline.Event, stream *clientruntime.TurnStream, err error)
}

type remoteSessionsMsg struct {
	sessions []clientruntime.SessionInfo
	err      error
}

type remoteSessionLoadedMsg struct {
	session clientruntime.SessionInfo
	history []timeline.Event
	stream  *clientruntime.TurnStream
	ctx     context.Context
	cancel  context.CancelFunc
	err     error
}

func listRemoteSessionsCmd(runtime remoteSessionRuntime) (cmd tea.Cmd) {
	return func() tea.Msg {
		sessions, err := runtime.ListSessions(context.Background())
		return remoteSessionsMsg{sessions: sessions, err: err}
	}
}

func followRemoteSessionCmd(runtime remoteSessionRuntime, sessionID string) (cmd tea.Cmd) {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		result := remoteSessionLoadedMsg{ctx: ctx, cancel: cancel}
		if sessionID != "" {
			if result.err = runtime.OpenSession(ctx, sessionID); result.err != nil {
				cancel()
				return result
			}
		}
		result.history, result.stream, result.err = runtime.FollowSession(ctx)
		result.session = runtime.Session()
		if result.err != nil || result.stream == nil {
			cancel()
		}
		return result
	}
}

func remoteSessionIdentity(session clientruntime.SessionInfo) (name string, location string) {
	project := strings.TrimSpace(session.ProjectName)
	if project == "" {
		project = "remote"
	}
	name = runtimeDisplayName(project, "remote")
	path := strings.TrimSpace(session.ProjectPath)
	if path == "" {
		path = "backend workspace"
	}
	location = fmt.Sprintf("%s · session %s", path, session.ID)
	return name, location
}
