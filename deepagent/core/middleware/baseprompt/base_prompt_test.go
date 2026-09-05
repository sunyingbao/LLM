package baseprompt

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBasePromptMiddleware(t *testing.T) {
	middleware := New("identity")
	require.Equal(t, BasePromptMiddlewareName, middleware.Name())
	messages, err := middleware.BuildInitialContext(context.Background())
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "identity", messages[0].Content)

	messages, err = New("").BuildInitialContext(context.Background())
	require.NoError(t, err)
	require.Nil(t, messages)
}
