package mcpinfra

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	bytedmcp "code.byted.org/inf/bytedmcp/go/client"
	einomcp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"eino-cli/deepagent/cloud/worker/bootstrap/config"
)

const (
	clientName    = "aic_agent_sdk_worker"
	clientVersion = "0.1.0"
)

// Runtime owns MCP clients and the tools derived from them.
type Runtime struct {
	tools   []tool.BaseTool
	closers []func() error
}

func Build(ctx context.Context, cfg config.MCPConfig) (*Runtime, error) {
	if !cfg.Enabled {
		return &Runtime{}, nil
	}
	r := &Runtime{}
	for _, server := range cfg.Servers {
		tools, closeFn, err := buildServerTools(ctx, cfg, server)
		if err != nil {
			_ = r.Close()
			return nil, fmt.Errorf("init mcp server %q: %w", server.Name, err)
		}
		r.tools = append(r.tools, tools...)
		if closeFn != nil {
			r.closers = append(r.closers, closeFn)
		}
	}
	if err := rejectDuplicateTools(ctx, r.tools); err != nil {
		_ = r.Close()
		return nil, err
	}
	return r, nil
}

func (r *Runtime) Tools() []tool.BaseTool {
	if r == nil {
		return nil
	}
	return append([]tool.BaseTool(nil), r.tools...)
}

func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	var err error
	for i := len(r.closers) - 1; i >= 0; i-- {
		err = errors.Join(err, r.closers[i]())
	}
	return err
}

func buildServerTools(ctx context.Context, cfg config.MCPConfig, server config.MCPServerConfig) ([]tool.BaseTool, func() error, error) {
	switch server.Type {
	case config.MCPServerTypeBytedHTTP:
		return buildBytedHTTPTools(ctx, cfg, server)
	case config.MCPServerTypeStdio:
		return buildStdioTools(ctx, cfg, server)
	default:
		return nil, nil, fmt.Errorf("unsupported mcp server type %q", server.Type)
	}
}

func buildBytedHTTPTools(ctx context.Context, cfg config.MCPConfig, server config.MCPServerConfig) ([]tool.BaseTool, func() error, error) {
	opts := []bytedmcp.BytedMCPClientOption{
		bytedmcp.WithRequestTimeout(requestTimeout(cfg, server)),
	}
	if region, ok := parseRegion(firstNonEmpty(server.Region, cfg.Region)); ok {
		opts = append(opts, bytedmcp.WithMCPGatewayRegion(region))
	}
	if len(server.Headers) > 0 {
		opts = append(opts, bytedmcp.WithCallToolMetaHeaders(server.Headers))
	}
	if len(server.Params) > 0 {
		opts = append(opts, bytedmcp.WithCallToolMetaParams(server.Params))
	}
	if server.Trace {
		opts = append(opts, bytedmcp.WithCallToolTraceEnabled())
	}
	cli, err := bytedmcp.NewBytedMCPClient([]bytedmcp.MCPClientConf{{
		ServerName: server.PSM,
		ClientType: bytedmcp.HTTP,
		PpeEnv:     server.PPEEnv,
	}}, opts...)
	if err != nil {
		return nil, nil, err
	}
	tools, err := toolsFromClients(ctx, cli.ListMCPClients(), server.Tools, server.Headers)
	if err != nil {
		_ = cli.Close()
		return nil, nil, err
	}
	return tools, cli.Close, nil
}

func buildStdioTools(ctx context.Context, cfg config.MCPConfig, server config.MCPServerConfig) ([]tool.BaseTool, func() error, error) {
	cli, err := mcpclient.NewStdioMCPClient(server.Command, stdioEnv(server.Env), server.Args...)
	if err != nil {
		return nil, nil, err
	}
	requestCtx, cancel := requestContext(ctx, cfg, server)
	defer cancel()
	if err := initialize(requestCtx, cli); err != nil {
		_ = cli.Close()
		return nil, nil, err
	}
	tools, err := einomcp.GetTools(requestCtx, &einomcp.Config{
		Cli:          cli,
		ToolNameList: server.Tools,
	})
	if err != nil {
		_ = cli.Close()
		return nil, nil, err
	}
	return tools, cli.Close, nil
}

func toolsFromClients(ctx context.Context, clients []bytedmcp.ExtendedMCPClient, names []string, headers map[string]string) ([]tool.BaseTool, error) {
	var out []tool.BaseTool
	for _, cli := range clients {
		tools, err := einomcp.GetTools(ctx, &einomcp.Config{
			Cli:           cli,
			ToolNameList:  names,
			CustomHeaders: headers,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, tools...)
	}
	return out, nil
}

func initialize(ctx context.Context, cli mcpclient.MCPClient) error {
	_, err := cli.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    clientName,
				Version: clientVersion,
			},
		},
	})
	return err
}

func stdioEnv(extra map[string]string) []string {
	env := append([]string(nil), os.Environ()...)
	keys := make([]string, 0, len(extra))
	for key := range extra {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		env = append(env, key+"="+extra[key])
	}
	return env
}

func requestTimeout(cfg config.MCPConfig, server config.MCPServerConfig) time.Duration {
	ms := server.RequestTimeoutMS
	if ms == 0 {
		ms = cfg.RequestTimeoutMS
	}
	if ms <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(ms) * time.Millisecond
}

func requestContext(ctx context.Context, cfg config.MCPConfig, server config.MCPServerConfig) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, requestTimeout(cfg, server))
}

func parseRegion(region string) (bytedmcp.McpGatewayRegion, bool) {
	switch strings.ToLower(strings.TrimSpace(region)) {
	case "":
		return bytedmcp.McpGatewayRegionInfer, false
	case "infer":
		return bytedmcp.McpGatewayRegionInfer, true
	case "cn", "china":
		return bytedmcp.McpGatewayRegionCn, true
	case "boe", "china_boe":
		return bytedmcp.McpGatewayRegionBoe, true
	case "i18n":
		return bytedmcp.McpGatewayRegionI18n, true
	case "i18n_boe", "i18n-boe":
		return bytedmcp.McpGatewayRegionI18nBoe, true
	case "usttp":
		return bytedmcp.McpGatewayRegionUsttp, true
	case "euttp":
		return bytedmcp.McpGatewayRegionEuttp, true
	case "i18n_bd", "i18n-bd":
		return bytedmcp.McpGatewayRegionI18nBD, true
	case "sandbox":
		return bytedmcp.McpGatewayRegionSandbox, true
	default:
		return bytedmcp.McpGatewayRegionInfer, false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func rejectDuplicateTools(ctx context.Context, tools []tool.BaseTool) error {
	seen := map[string]struct{}{}
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil {
			return fmt.Errorf("read mcp tool info: %w", err)
		}
		if info == nil || strings.TrimSpace(info.Name) == "" {
			return fmt.Errorf("mcp tool has empty name")
		}
		if _, ok := seen[info.Name]; ok {
			return fmt.Errorf("mcp tool %q is duplicated", info.Name)
		}
		seen[info.Name] = struct{}{}
	}
	return nil
}
