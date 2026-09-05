package event

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEventTypeString(t *testing.T) {
	require.Equal(t, "TURN_STARTED", EventTypeTurnStarted.String())
}
