package application

import (
	"context"
	"sync"
	"time"
)

// LocalQueue triggers durable local jobs; LocalJobs remains the source of truth.
type LocalQueue struct {
	jobs      *LocalJobs
	publisher MessagePublisher
	pending   chan string
	done      chan struct{}
	once      sync.Once
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func NewLocalQueue(jobs *LocalJobs, publisher MessagePublisher) *LocalQueue {
	return &LocalQueue{
		jobs:      jobs,
		publisher: publisher,
		pending:   make(chan string, 128),
		done:      make(chan struct{}),
	}
}

func (queue *LocalQueue) Start() {
	queue.once.Do(func() {
		queue.wg.Add(1)
		go func() {
			defer queue.wg.Done()
			for {
				select {
				case jobID := <-queue.pending:
					queue.process(jobID)
				case <-queue.done:
					return
				}
			}
		}()
	})
}

func (queue *LocalQueue) Close() {
	if queue == nil {
		return
	}
	queue.closeOnce.Do(func() {
		close(queue.done)
		queue.wg.Wait()
	})
}

func (queue *LocalQueue) Enqueue(jobID string) {
	select {
	case queue.pending <- jobID:
	case <-queue.done:
	}
}

func (queue *LocalQueue) Recover() error {
	jobIDs, err := queue.jobs.PendingDelivery()
	if err != nil {
		return err
	}
	for _, jobID := range jobIDs {
		queue.Enqueue(jobID)
	}
	return nil
}

func (queue *LocalQueue) process(jobID string) {
	job, err := queue.jobs.Complete(jobID)
	if err != nil || queue.publisher == nil {
		return
	}
	if err := queue.publish(job); err != nil {
		queue.retry(jobID)
		return
	}
	_ = queue.jobs.MarkDelivered(jobID)
}

func (queue *LocalQueue) publish(job LocalJob) error {
	message := CallbackMessage{Provider: localJobProvider(job.Kind), EventID: "local:" + job.ID, JobID: job.ID}
	publishContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return queue.publisher.Publish(publishContext, message)
}

func (queue *LocalQueue) retry(jobID string) {
	select {
	case <-time.After(time.Second):
		queue.Enqueue(jobID)
	case <-queue.done:
	}
}
