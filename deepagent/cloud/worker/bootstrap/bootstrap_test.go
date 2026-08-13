package bootstrap

import (
	"testing"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
)

func TestOptionsKeepBusinessExtensionsAtBootstrapBoundary(t *testing.T) {
	var businessTool tool.BaseTool
	var businessCallback callbacks.Handler
	opts := Options{
		Args:      []string{"-conf", "conf/worker.local.yml"},
		Tools:     []tool.BaseTool{businessTool},
		Callbacks: []callbacks.Handler{businessCallback},
	}

	if len(opts.Args) != 2 || len(opts.Tools) != 1 || len(opts.Callbacks) != 1 {
		t.Fatalf("bootstrap options lost business extensions: %+v", opts)
	}
}

func TestProfileConfigFileNamesAreExplicit(t *testing.T) {
	if got := ProfileConfigFilename(ProfileLocal); got != "worker.local.yml" {
		t.Fatalf("local profile filename=%q", got)
	}
	if got := ProfileConfigFilename(ProfileRemote); got != "worker.remote.yml" {
		t.Fatalf("remote profile filename=%q", got)
	}
}
