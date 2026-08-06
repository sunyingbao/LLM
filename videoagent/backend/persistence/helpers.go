package persistence

import (
	"fmt"
	"sync/atomic"
	"time"
)

var idSequence atomic.Uint64

func newID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UTC().UnixNano(), idSequence.Add(1))
}

func newSubmitKey(runID, nodeID, instanceKey string) string {
	return fmt.Sprintf("%s:%s:%s", runID, nodeID, instanceKey)
}
