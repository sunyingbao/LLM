.PHONY: videoagent-build videoagent-test videoagent-remote-e2e

videoagent-build:
	mkdir -p bin
	GOTOOLCHAIN=go1.25.0 go build -mod=readonly -tags 'fornax bytedance' -o bin/videoagent ./cmd/videoagent

videoagent-test:
	GOTOOLCHAIN=go1.25.0 go test -mod=readonly ./backend/videoagent ./cmd/videoagent

videoagent-remote-e2e:
	GOTOOLCHAIN=go1.25.0 VIDEO_AGENT_REMOTE_E2E=1 go test -mod=readonly -tags 'fornax bytedance' ./cmd/videoagent -run TestRemoteVideoAgentEndToEnd -count=1 -v -timeout=60m
