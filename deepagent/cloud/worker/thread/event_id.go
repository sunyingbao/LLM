//go:build !windows

package thread

import (
	"fmt"
	"time"
)

func (t *Runtime) eventID(turnID string) string {
	return fmt.Sprintf("evt_%s_%s_%d", t.threadID, turnID, time.Now().UnixNano())
}
