package tasktool

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestThreadProfileEmpty(t *testing.T) {
	require.True(t, (ThreadProfile{}).Empty())
	require.False(t, (ThreadProfile{Role: "reviewer"}).Empty())
	require.False(t, (ThreadProfile{Cwd: "/workspace"}).Empty())
}
