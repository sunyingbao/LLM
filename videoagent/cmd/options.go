package main

import (
	"encoding/json"
	"flag"
	"io"
	"os"

	videomodel "eino-cli/videoagent/backend/model"
)

type runOptions struct {
	remoteConfigPath string
	modelConfigPath  string
	promptConfigPath string
	credentialsPath  string
}

func parseRunOptions(args []string) (runOptions, error) {
	options := runOptions{}
	flags := flag.NewFlagSet("videoagent", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.remoteConfigPath, "remote-config", "", "JSON config for direct image/TTS/video clients")
	flags.StringVar(&options.modelConfigPath, "model-config", "", "JSON config for the optional ReAct chat model")
	flags.StringVar(&options.promptConfigPath, "prompt-config", "", "JSON config for managed workflow prompts")
	flags.StringVar(&options.credentialsPath, "credentials-config", "", "untracked JSON credentials for Fornax and MaaS models")
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
