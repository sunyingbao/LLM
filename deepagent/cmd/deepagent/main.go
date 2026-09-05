package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"code.byted.org/gopkg/logs/v2"
	"code.byted.org/gopkg/logs/v2/writer"
	backendconfig "eino-cli/deepagent/backend/config"
	clientruntime "eino-cli/deepagent/host/runtime"
	sdkruntime "eino-cli/deepagent/runtime"
)

type CLIOptions struct {
	RootDir         string
	WorkDir         string
	ResumeThreadID  string
	ResumeSessionID string
	Prompt          string
	ReadFromStdin   bool
	AutoResume      bool
}

func parseFlags() (cfg CLIOptions) {
	flag.StringVar(&cfg.RootDir, "root", "", "LLM repository root (defaults to DEEPAGENT_ROOT or current directory)")
	flag.StringVar(&cfg.WorkDir, "workdir", ".", "工作目录")
	flag.StringVar(&cfg.ResumeThreadID, "resume_thread_id", "", "启动时恢复指定 root thread")
	flag.StringVar(&cfg.ResumeSessionID, "resume_session_id", "", "启动时恢复指定 backend session")
	flag.StringVar(&cfg.Prompt, "prompt", "", "单次运行的 prompt 文本")
	flag.BoolVar(&cfg.ReadFromStdin, "stdin", false, "从标准输入读取单次运行 prompt")
	flag.BoolVar(&cfg.AutoResume, "auto_resume", false, "交互模式启动时自动恢复当前 user/workdir 最新 root session")
	flag.Parse()
	return cfg
}

func (c CLIOptions) oneShotPrompt(args []string, stdin io.Reader) (prompt string, err error) {
	var sources []string
	if strings.TrimSpace(c.Prompt) != "" {
		sources = append(sources, "prompt flag")
	}
	if c.ReadFromStdin {
		sources = append(sources, "stdin")
	}
	if len(args) > 0 {
		sources = append(sources, "args")
	}
	if len(sources) > 1 {
		return "", fmt.Errorf("单次运行输入来源冲突: %s", strings.Join(sources, ", "))
	}
	if strings.TrimSpace(c.Prompt) != "" {
		return c.Prompt, nil
	}
	if c.ReadFromStdin {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("读取 stdin 失败: %w", err)
		}
		prompt := strings.TrimRight(string(data), "\r\n")
		if strings.TrimSpace(prompt) == "" {
			return "", fmt.Errorf("stdin 为空")
		}
		return prompt, nil
	}
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}
	return "", nil
}

func initLogger() {
	options := []logs.Option{
		logs.SetWriter(
			logs.InfoLevel,
			writer.NewAsyncWriter(
				writer.NewFileWriter("./logs/deepagent.log", writer.Hourly, writer.SetKeepFiles(12)),
				true,
			),
		),
		logs.SetCallDepth(2),
	}
	logs.SetDefaultLogger(options...)
}

func main() {
	cfg := parseFlags()
	runtimeKind, err := clientruntime.RuntimeKindFromEnv()
	if err != nil {
		fmt.Printf("解析 runtime 失败: %v\n", err)
		os.Exit(1)
	}
	if runtimeKind == sdkruntime.RuntimeRemote {
		if err = runCLI(context.Background(), nil, cfg, flag.Args(), os.Stdin, os.Stdout); err != nil {
			fmt.Printf("运行 CLI 失败: %v\n", err)
			os.Exit(1)
		}
		return
	}
	root, err := repositoryRootFrom(cfg.RootDir)
	if err != nil {
		fmt.Printf("解析仓库根目录失败: %v\n", err)
		os.Exit(1)
	}
	if err := os.Chdir(root); err != nil {
		fmt.Printf("进入仓库根目录失败: %v\n", err)
		os.Exit(1)
	}
	loadedConfig, err := backendconfig.Load(root)
	if err != nil {
		fmt.Printf("加载 yaml/config.yaml 失败: %v\n", err)
		os.Exit(1)
	}
	backendconfig.SetLogLevel(loadedConfig)
	cfg.RootDir = root
	initLogger()

	if err = runCLI(context.Background(), loadedConfig, cfg, flag.Args(), os.Stdin, os.Stdout); err != nil {
		fmt.Printf("运行 CLI 失败: %v\n", err)
		os.Exit(1)
	}
}

func repositoryRootFrom(flagRoot string) (root string, err error) {
	root = strings.TrimSpace(flagRoot)
	if root == "" {
		root = strings.TrimSpace(os.Getenv("DEEPAGENT_ROOT"))
	}
	if root == "" {
		root, err = os.Getwd()
		if err != nil {
			return "", err
		}
		return findRepositoryRoot(root), nil
	}
	return filepath.Abs(root)
}

// findRepositoryRoot makes IDE launches from deepagent/cmd/deepagent behave
// like launches from the repository root without changing explicit overrides.
func findRepositoryRoot(start string) (root string) {
	root = filepath.Clean(start)
	for {
		if _, err := os.Stat(filepath.Join(root, "yaml", "config.yaml")); err == nil {
			return root
		}
		parent := filepath.Dir(root)
		if parent == root {
			return filepath.Clean(start)
		}
		root = parent
	}
}
