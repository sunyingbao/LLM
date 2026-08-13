package execute

import (
	"context"

	sdkutils "eino-cli/deepagent/core/utils"
)

type OutputFormatter interface {
	Format(ctx context.Context, result ExecCommandOutput) string
}

type JSONOutputFormatter struct{}

func NewJSONOutputFormatter() *JSONOutputFormatter {
	return &JSONOutputFormatter{}
}

func (f *JSONOutputFormatter) Format(ctx context.Context, result ExecCommandOutput) string {
	return sdkutils.ToString(result)
}

func maxOutputBytes(cfg Config) int {
	if cfg.MaxOutputBytes > 0 {
		return cfg.MaxOutputBytes
	}
	return defaultMaxOutputSize
}
