package videoagent

import "context"

func useDirectCallbackPublisher(application *LocalApplication) {
	application.SetMessageQueue(MessagePublisherFunc(func(ctx context.Context, message CallbackMessage) error {
		return application.Runner.ProcessCallback(ctx, message)
	}), nil)
}
