package videoagent

import (
	"context"

	"eino-cli/videoagent/backend/messaging"
)

type NATSConfig = messaging.NATSConfig
type NATSMessageBus = messaging.NATSMessageBus
type AllowAllCallbackVerifier = messaging.AllowAllCallbackVerifier
type HMACCallbackVerifier = messaging.HMACCallbackVerifier

const (
	DefaultNATSURL      = messaging.DefaultNATSURL
	DefaultNATSStream   = messaging.DefaultNATSStream
	DefaultNATSSubject  = messaging.DefaultNATSSubject
	DefaultNATSConsumer = messaging.DefaultNATSConsumer
)

func NewNATSMessageBus(ctx context.Context, config NATSConfig) (*NATSMessageBus, error) {
	return messaging.NewNATSMessageBus(ctx, config)
}

func parseCallbackMessageWithEventID(provider string, body []byte, eventID string) (CallbackMessage, error) {
	return messaging.ParseCallbackMessageWithEventID(provider, body, eventID)
}
