//go:build !fornax

package videoagent

import "fmt"

func NewFornaxPromptExecutor(FornaxConfig) (PromptExecutor, error) {
	return nil, fmt.Errorf("native Fornax prompt executor is disabled; rebuild with -tags fornax")
}
