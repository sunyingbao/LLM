package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"eino-cli/backend/videoagent"
)

func main() {
	address := flag.String("addr", "127.0.0.1:18080", "HTTP listen address")
	dataDir := flag.String("data", ".video-agent", "local workflow data directory")
	flag.Parse()

	application, err := videoagent.NewLocalApplication(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer application.Close()
	if err := application.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
	log.Printf("video agent listening on http://%s", *address)
	log.Fatal(http.ListenAndServe(*address, videoagent.NewHTTPHandler(application)))
}
