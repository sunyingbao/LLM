.PHONY: videoagent-build videoagent-test videoagent-tidy videoagent-requirement-smoke videoagent-remote-e2e

videoagent-build:
	mkdir -p bin
	GOTOOLCHAIN=go1.25.0 go build -mod=readonly -tags 'fornax bytedance' -o bin/videoagent ./cmd/videoagent

videoagent-test:
	GOTOOLCHAIN=go1.25.0 go test -mod=readonly ./videoagent/backend/... ./cmd/videoagent

videoagent-tidy:
	GOTOOLCHAIN=go1.25.0 go mod tidy

videoagent-requirement-smoke:
	GOTOOLCHAIN=go1.25.0 VIDEO_AGENT_REQUIREMENT_SMOKE=1 VIDEO_AGENT_CREDENTIALS_CONFIG=$${VIDEO_AGENT_CREDENTIALS_CONFIG:-$(CURDIR)/configs/videoagent/credentials.local.json} go test -mod=readonly -tags fornax ./cmd/videoagent -run TestRequirementModelSmoke -count=1 -v -timeout=3m

videoagent-remote-e2e:
	GOTOOLCHAIN=go1.25.0 VIDEO_AGENT_REMOTE_E2E=1 go test -mod=readonly -tags 'fornax bytedance' ./cmd/videoagent -run TestRemoteVideoAgentEndToEnd -count=1 -v -timeout=60m
