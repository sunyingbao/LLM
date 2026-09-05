package webui

import "embed"

// Static contains the browser console served by deep_agent_sdk.
//
//go:embed static/*
var Static embed.FS
