package network

import (
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetFreePort(t *testing.T) {
	port, err := GetFreePort(0)
	require.NoError(t, err)
	require.Positive(t, port)

	listener, err := net.Listen("tcp", "0.0.0.0:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })
	occupiedPort := listener.Addr().(*net.TCPAddr).Port
	require.False(t, isAvailable(occupiedPort))

	port, err = GetFreePort(occupiedPort)
	require.NoError(t, err)
	require.NotEqual(t, occupiedPort, port)
	require.True(t, isAvailable(port))
	require.NotEmpty(t, fmt.Sprintf("%d", port))
}
