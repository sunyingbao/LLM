package videoagent

import "context"

type imageClientWithURL struct {
	ImageClient
	resolver MediaURLResolver
}

func withImageURL(client ImageClient, resolver MediaURLResolver) ImageClient {
	if client == nil || resolver == nil {
		return client
	}
	return &imageClientWithURL{ImageClient: client, resolver: resolver}
}

func (client *imageClientWithURL) SubmitImage(ctx context.Context, request ImageRequest) (SubmittedJob, error) {
	job, err := client.ImageClient.SubmitImage(ctx, request)
	if err != nil || job.Status == nil {
		return job, err
	}
	status, err := client.resolve(ctx, *job.Status)
	job.Status = &status
	return job, err
}

func (client *imageClientWithURL) GetImage(ctx context.Context, jobID string) (JobStatus, error) {
	status, err := client.ImageClient.GetImage(ctx, jobID)
	if err != nil {
		return status, err
	}
	return client.resolve(ctx, status)
}

func (client *imageClientWithURL) FindImageBySubmitKey(ctx context.Context, key string) (SubmittedJob, bool, error) {
	job, found, err := client.ImageClient.FindImageBySubmitKey(ctx, key)
	if err != nil || !found || job.Status == nil {
		return job, found, err
	}
	status, err := client.resolve(ctx, *job.Status)
	job.Status = &status
	return job, found, err
}

func (client *imageClientWithURL) CancelImage(ctx context.Context, jobID string) error {
	canceler, ok := client.ImageClient.(ImageCanceler)
	if !ok {
		return ErrCancellationUnsupported
	}
	return canceler.CancelImage(ctx, jobID)
}

func (client *imageClientWithURL) resolve(ctx context.Context, status JobStatus) (JobStatus, error) {
	if status.State != JobSucceeded || status.URL != "" || status.URI == "" {
		return status, nil
	}
	url, err := client.resolver.ResolveURL(ctx, status.URI)
	if err != nil {
		return status, err
	}
	status.URL = url
	return status, nil
}
