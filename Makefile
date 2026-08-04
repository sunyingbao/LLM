.PHONY: videoagent-build videoagent-test

videoagent-build:
	mkdir -p bin
	GOTOOLCHAIN=go1.25.0 go build -mod=readonly -tags 'fornax bytedance' -o bin/videoagent ./cmd/videoagent

videoagent-test:
	GOTOOLCHAIN=go1.25.0 go test -mod=readonly ./backend/videoagent ./cmd/videoagent
