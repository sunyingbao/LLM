package agentthread

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHistoryRecordOrderSeq(t *testing.T) {
	var record *HistoryRecord
	require.Zero(t, record.OrderSeq())
	require.Equal(t, int64(7), (&HistoryRecord{Seq: 7, MessageID: 9}).OrderSeq())
	require.Equal(t, int64(9), (&HistoryRecord{MessageID: 9}).OrderSeq())
}
