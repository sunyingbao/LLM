package clienttest

import (
	"testing"

	runtimeclient "eino-cli/deepagent/runtime"
)

func TestFakeSatisfiesRuntimeClientContract(t *testing.T) {
	Run(t, func(t *testing.T) (client runtimeclient.Client, cleanup func()) {
		return NewFake(), func() {}
	})
}
