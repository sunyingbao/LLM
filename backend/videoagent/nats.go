package videoagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	DefaultNATSURL      = nats.DefaultURL
	DefaultNATSStream   = "VIDEO_AGENT_CALLBACKS"
	DefaultNATSSubject  = "video_agent.callbacks"
	DefaultNATSConsumer = "video_agent_runner"
	defaultNATSMaxRetry = 20
)

type NATSConfig struct {
	URL               string
	Stream            string
	Subject           string
	Consumer          string
	DeadLetterSubject string
	MaxRetries        uint64
}

// NATSMessageBus persists callback messages and resumes runs through one durable consumer.
type NATSMessageBus struct {
	connection        *nats.Conn
	jetStream         jetstream.JetStream
	consumer          jetstream.Consumer
	subject           string
	deadLetterSubject string
	maxRetries        uint64
	consumeMu         sync.Mutex
	consuming         bool
	closeOnce         sync.Once
	closeErr          error
	closed            atomic.Bool
}

func NewNATSMessageBus(ctx context.Context, config NATSConfig) (*NATSMessageBus, error) {
	config = defaultNATSConfig(config)
	connection, err := nats.Connect(config.URL, nats.Name("video-agent"), nats.Timeout(5*time.Second), nats.DrainTimeout(5*time.Second))
	if err != nil {
		return nil, err
	}
	js, err := jetstream.New(connection)
	if err != nil {
		connection.Close()
		return nil, err
	}
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       config.Stream,
		Subjects:   []string{config.Subject, config.DeadLetterSubject},
		Retention:  jetstream.WorkQueuePolicy,
		Storage:    jetstream.FileStorage,
		MaxAge:     7 * 24 * time.Hour,
		Duplicates: 24 * time.Hour,
	})
	if err != nil {
		connection.Close()
		return nil, err
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       config.Consumer,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxAckPending: 64,
		FilterSubject: config.Subject,
	})
	if err != nil {
		connection.Close()
		return nil, err
	}
	return &NATSMessageBus{
		connection:        connection,
		jetStream:         js,
		consumer:          consumer,
		subject:           config.Subject,
		deadLetterSubject: config.DeadLetterSubject,
		maxRetries:        config.MaxRetries,
	}, nil
}

func (bus *NATSMessageBus) Publish(ctx context.Context, message CallbackMessage) error {
	if bus == nil || bus.jetStream == nil || bus.closed.Load() {
		return fmt.Errorf("nats message bus is nil")
	}
	if strings.TrimSpace(message.Provider) == "" || strings.TrimSpace(message.EventID) == "" ||
		(strings.TrimSpace(message.JobID) == "" && strings.TrimSpace(message.SubmitKey) == "") {
		return fmt.Errorf("callback provider, event id and job id or submit key are required")
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	_, err = bus.jetStream.Publish(ctx, bus.subject, payload, jetstream.WithMsgID(callbackReceiptKey(message)))
	return err
}

func (bus *NATSMessageBus) Consume(ctx context.Context, handle func(context.Context, CallbackMessage) error) error {
	if bus == nil || bus.consumer == nil {
		return fmt.Errorf("nats message bus is nil")
	}
	if handle == nil {
		return fmt.Errorf("callback handler is nil")
	}
	bus.consumeMu.Lock()
	if bus.consuming {
		bus.consumeMu.Unlock()
		return fmt.Errorf("nats consumer is already running")
	}
	bus.consuming = true
	bus.consumeMu.Unlock()
	defer func() {
		bus.consumeMu.Lock()
		bus.consuming = false
		bus.consumeMu.Unlock()
	}()

	for {
		err := bus.consumeMessages(ctx, handle)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if bus.closed.Load() {
			return nil
		}
		log.Printf("restart NATS callback consumer after error: %v", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (bus *NATSMessageBus) consumeMessages(ctx context.Context, handle func(context.Context, CallbackMessage) error) error {
	messages, err := bus.consumer.Messages(jetstream.PullMaxMessages(1))
	if err != nil {
		return err
	}
	done := make(chan struct{})
	defer close(done)
	defer messages.Stop()
	go func() {
		select {
		case <-ctx.Done():
			messages.Stop()
		case <-done:
		}
	}()

	for {
		message, err := messages.Next()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, jetstream.ErrMsgIteratorClosed) {
				return fmt.Errorf("nats message iterator closed")
			}
			return err
		}
		var callback CallbackMessage
		if err := json.Unmarshal(message.Data(), &callback); err != nil {
			_ = message.TermWithReason("invalid callback payload")
			log.Printf("discard invalid NATS callback: %v", err)
			continue
		}
		stopHeartbeat := make(chan struct{})
		go keepNATSMessageAlive(message, stopHeartbeat)
		handleErr := handle(ctx, callback)
		close(stopHeartbeat)
		if handleErr != nil {
			metadata, metadataErr := message.Metadata()
			if !isPollingMessage(callback) && !errors.Is(handleErr, ErrJobPending) && metadataErr == nil && metadata.NumDelivered >= bus.maxRetries {
				if deadLetterErr := bus.publishDeadLetter(ctx, callback, message.Data()); deadLetterErr == nil {
					if termErr := message.TermWithReason("callback retries exhausted"); termErr == nil {
						log.Printf("move NATS callback to dead letter event=%s job=%s after %d attempts: %v", callback.EventID, callback.JobID, metadata.NumDelivered, handleErr)
						continue
					}
				}
			}
			_ = message.NakWithDelay(callbackRetryDelay(metadata))
			log.Printf("retry NATS callback event=%s job=%s: %v", callback.EventID, callback.JobID, handleErr)
			continue
		}
		ackContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = message.DoubleAck(ackContext)
		cancel()
		if err != nil {
			log.Printf("ack NATS callback event=%s job=%s failed: %v", callback.EventID, callback.JobID, err)
		}
	}
}

func keepNATSMessageAlive(message jetstream.Msg, stop <-chan struct{}) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = message.InProgress()
		case <-stop:
			return
		}
	}
}

func (bus *NATSMessageBus) publishDeadLetter(ctx context.Context, callback CallbackMessage, payload []byte) error {
	_, err := bus.jetStream.Publish(
		ctx,
		bus.deadLetterSubject,
		payload,
		jetstream.WithMsgID("dead:"+callbackReceiptKey(callback)),
	)
	return err
}

func callbackRetryDelay(metadata *jetstream.MsgMetadata) time.Duration {
	if metadata == nil || metadata.NumDelivered <= 1 {
		return time.Second
	}
	delay := time.Duration(metadata.NumDelivered) * time.Second
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}

func (bus *NATSMessageBus) Close() error {
	if bus == nil || bus.connection == nil {
		return nil
	}
	bus.closeOnce.Do(func() {
		bus.closed.Store(true)
		bus.closeErr = bus.connection.Drain()
		bus.connection.Close()
	})
	return bus.closeErr
}

func defaultNATSConfig(config NATSConfig) NATSConfig {
	if strings.TrimSpace(config.URL) == "" {
		config.URL = DefaultNATSURL
	}
	if strings.TrimSpace(config.Stream) == "" {
		config.Stream = DefaultNATSStream
	}
	if strings.TrimSpace(config.Subject) == "" {
		config.Subject = DefaultNATSSubject
	}
	if strings.TrimSpace(config.Consumer) == "" {
		config.Consumer = DefaultNATSConsumer
	}
	if strings.TrimSpace(config.DeadLetterSubject) == "" {
		config.DeadLetterSubject = config.Subject + ".dead"
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = defaultNATSMaxRetry
	}
	return config
}

var _ MessagePublisher = (*NATSMessageBus)(nil)
var _ MessageConsumer = (*NATSMessageBus)(nil)
