package main

import (
	"encoding/json"
	"flag"
	"io"
	"os"

	"eino-cli/videoagent/backend/messaging"
	videomodel "eino-cli/videoagent/backend/model"
)

const defaultCanvasAgentModelKey = "aic.aic_tool.user_req_analysis"

type runOptions struct {
	address          string
	dataDir          string
	remoteConfigPath string
	modelConfigPath  string
	promptConfigPath string
	credentialsPath  string
	mongoURI         string
	natsURL          string
}

func parseRunOptions(args []string) (runOptions, error) {
	options := runOptions{
		address:  "127.0.0.1:18080",
		dataDir:  ".video-agent",
		mongoURI: "mongodb://127.0.0.1:27017",
		natsURL:  messaging.DefaultNATSURL,
	}
	flags := flag.NewFlagSet("videoagent", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.address, "addr", options.address, "HTTP listen address")
	flags.StringVar(&options.dataDir, "data", options.dataDir, "local workflow data directory")
	flags.StringVar(&options.remoteConfigPath, "remote-config", "", "JSON config for direct image/TTS/video clients")
	flags.StringVar(&options.modelConfigPath, "model-config", "", "JSON config for the optional ReAct chat model")
	flags.StringVar(&options.promptConfigPath, "prompt-config", "", "JSON config for managed workflow prompts")
	flags.StringVar(&options.credentialsPath, "credentials-config", "", "untracked JSON credentials for Fornax and MaaS models")
	flags.StringVar(&options.mongoURI, "mongo-uri", options.mongoURI, "MongoDB URI; empty disables MongoDB state")
	flags.StringVar(&options.natsURL, "nats-url", options.natsURL, "NATS server URL")
	if err := flags.Parse(args); err != nil {
		return runOptions{}, err
	}
	return options, nil
}

func loadCredentials(path string) (*videomodel.CredentialsConfig, error) {
	if path == "" {
		return nil, nil
	}
	loaded, err := readJSON[videomodel.CredentialsConfig](path)
	if err != nil {
		return nil, err
	}
	return &loaded, nil
}

func readJSON[T any](path string) (T, error) {
	var value T
	payload, err := os.ReadFile(path)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return value, err
	}
	return value, nil
}
