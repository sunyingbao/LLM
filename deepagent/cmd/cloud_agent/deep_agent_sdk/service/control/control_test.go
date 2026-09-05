package control

import (
	"testing"

	cloudapi "eino-cli/deepagent/cloud/api"
	httpcommon "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/hertz_gen/deep_agent_sdk_common"
	"github.com/stretchr/testify/require"
)

func TestControlConversions(t *testing.T) {
	require.Zero(t, mainThreadID(nil))
	require.Zero(t, mainThreadID(&httpcommon.AgentSessionView{}))
	threadID := int64(42)
	require.Equal(t, threadID, mainThreadID(&httpcommon.AgentSessionView{
		Session: &httpcommon.AgentSession{MainThreadID: &threadID},
	}))

	refs := messageRefs([]cloudapi.MessageRef{
		{ThreadID: "17", MessageID: "message-1"},
		{ThreadID: "invalid", MessageID: "message-2"},
	})
	require.Equal(t, []*httpcommon.MessageRef{
		{ThreadID: 17, MessageID: "message-1"},
		{ThreadID: 0, MessageID: "message-2"},
	}, refs)
}
