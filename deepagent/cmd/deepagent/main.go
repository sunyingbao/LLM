package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"code.byted.org/gopkg/logs/v2"
	"code.byted.org/gopkg/logs/v2/writer"
)

const defaultUserID int64 = 1

type AppConfig struct {
	UserID         int64
	WorkDir        string
	SkillsDir      string
	ResumeThreadID string
	DBPath         string
	CheckpointDir  string
	EnableWeb      bool
	Prompt         string
	ReadFromStdin  bool
	AutoResume     bool
	EnableRG       bool
}

func parseFlags() AppConfig {
	var cfg AppConfig
	flag.Int64Var(&cfg.UserID, "user_id", defaultUserID, "本地用户 ID，用于隔离 thread/session")
	flag.StringVar(&cfg.WorkDir, "workdir", ".", "工作目录")
	flag.StringVar(&cfg.SkillsDir, "skills", "", "技能目录路径")
	flag.StringVar(&cfg.ResumeThreadID, "resume_thread_id", "", "启动时恢复指定 root thread")
	flag.StringVar(&cfg.DBPath, "db_path", "./.deepagent/agentthread.db", "SQLite 持久化路径")
	flag.StringVar(&cfg.CheckpointDir, "checkpoint_dir", "./.deepagent/checkpoints", "checkpoint 目录")
	flag.BoolVar(&cfg.EnableWeb, "web", true, "启用 Web 能力")
	flag.StringVar(&cfg.Prompt, "prompt", "", "单次运行的 prompt 文本")
	flag.BoolVar(&cfg.ReadFromStdin, "stdin", false, "从标准输入读取单次运行 prompt")
	flag.BoolVar(&cfg.AutoResume, "auto_resume", false, "交互模式启动时自动恢复当前 user/workdir 最新 root session")
	flag.Parse()
	return cfg
}

func (c AppConfig) oneShotPrompt(args []string, stdin io.Reader) (string, error) {
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
	initLogger()
	cfg := parseFlags()

	app, err := NewApp(context.Background(), cfg)
	if err != nil {
		fmt.Printf("初始化失败: %v\n", err)
		os.Exit(1)
	}
	os.Exit(app.Run(context.Background(), flag.Args()))
}
