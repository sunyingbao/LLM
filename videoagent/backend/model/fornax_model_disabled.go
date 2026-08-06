//go:build !fornax

package model

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
)

func newFornaxChatModel(context.Context, ChatModelConfig) (model.ToolCallingChatModel, error) {
	return nil, fmt.Errorf("native Fornax model is disabled; rebuild with -tags fornax")
}
