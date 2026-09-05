package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestApprovalKeepsReadingStream(t *testing.T) {
	stream := make(chan tea.Msg, 1)
	stream <- chunkMsg("after approval")
	m := &Model{streaming: true, streamCh: stream}
	_, cmd := m.Update(approvalRequest{toolName: "execute", reply: make(chan bool, 1)})
	if cmd == nil {
		t.Fatal("approval stopped the stream reader")
	}
	if msg := cmd(); msg != chunkMsg("after approval") {
		t.Fatalf("next stream message = %#v", msg)
	}
}
