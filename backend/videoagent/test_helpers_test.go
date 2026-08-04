package videoagent

import "context"

func useDirectCallbackPublisher(application *LocalApplication) {
	application.SetMessageQueue(MessagePublisherFunc(func(ctx context.Context, message CallbackMessage) error {
		return application.Runner.OnCallback(ctx, message.Provider, message.EventID, message.JobID)
	}), nil)
}
