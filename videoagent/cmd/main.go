package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	app "eino-cli/videoagent/backend/application"
)

const defaultHTTPAddress = "127.0.0.1:18080"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	options, err := parseRunOptions(os.Args[1:])
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	credentials, err := loadCredentials(options.credentialsPath)
	if err != nil {
		return err
	}
	messageBus, err := newMessageBus(ctx)
	if err != nil {
		return err
	}
	defer logCloseError("NATS message bus", messageBus.Close)

	application, err := newApplication(ctx, options, credentials)
	if err != nil {
		return err
	}
	defer application.Close()
	application.SetMessageQueue(messageBus, messageBus)
	if err := application.Start(ctx); err != nil {
		return err
	}
	return serve(ctx, defaultHTTPAddress, app.NewHTTPHandler(application))
}
