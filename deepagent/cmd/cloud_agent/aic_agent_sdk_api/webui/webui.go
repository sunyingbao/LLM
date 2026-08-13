package webui

import "embed"

// Static contains the browser console served by aic_agent_sdk_api.
//
//go:embed static/*
var Static embed.FS
