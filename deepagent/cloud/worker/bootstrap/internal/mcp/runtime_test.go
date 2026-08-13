package mcpinfra

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"eino-cli/deepagent/cloud/worker/bootstrap/config"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestBuildDisabled(t *testing.T) {
	rt, err := Build(context.Background(), config.MCPConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rt.Tools()) != 0 {
		t.Fatalf("tools len=%d", len(rt.Tools()))
	}
}

func TestRejectDuplicateTools(t *testing.T) {
	err := rejectDuplicateTools(context.Background(), []tool.BaseTool{
		fakeTool{name: "same"},
		fakeTool{name: "same"},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("err=%v", err)
	}
}

func TestStdioEnvIncludesConfiguredValues(t *testing.T) {
	env := stdioEnv(map[string]string{"MCP_TOKEN": "token"})
	found := false
	for _, item := range env {
		if item == "MCP_TOKEN=token" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("MCP_TOKEN not found in env")
	}
}

func TestBuildStdioTools(t *testing.T) {
	rt, err := Build(context.Background(), config.MCPConfig{
		Enabled: true,
		Servers: []config.MCPServerConfig{{
			Name:    "helper",
			Type:    config.MCPServerTypeStdio,
			Command: os.Args[0],
			Args:    []string{"-test.run=TestMCPHelperProcess", "--"},
			Env:     map[string]string{"GO_WANT_MCP_HELPER_PROCESS": "1"},
			Tools:   []string{"echo"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatalf("close runtime: %v", err)
		}
	}()
	tools := rt.Tools()
	if len(tools) != 1 {
		t.Fatalf("tools len=%d", len(tools))
	}
	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "echo" {
		t.Fatalf("tool name=%q", info.Name)
	}
}

func TestRequestContextUsesServerTimeout(t *testing.T) {
	ctx, cancel := requestContext(context.Background(), config.MCPConfig{RequestTimeoutMS: 5000}, config.MCPServerConfig{RequestTimeoutMS: 50})
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("request context should have deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > time.Second {
		t.Fatalf("deadline remaining = %s, want short server timeout", remaining)
	}
}

func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER_PROCESS") != "1" {
		return
	}
	s := server.NewMCPServer("helper", "1.0.0")
	s.AddTool(mcp.NewTool("echo"), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	if err := server.ServeStdio(s); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

type fakeTool struct {
	name string
}

func (f fakeTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: f.name}, nil
}

func (f fakeTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return "", nil
}
