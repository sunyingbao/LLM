package agentthread

import (
	"eino-cli/deepagent/core/graph"
	"github.com/cloudwego/eino/schema"
)

// runStatus is the lifecycle state of one logical turn. A run is still
// considered active while ending so a concurrent submit cannot accidentally
// start a second turn before the current one has finished draining events.
type runStatus string

const (
	runStatusRunning runStatus = "running"
	runStatusEnding  runStatus = "ending"
	runStatusEnded   runStatus = "ended"
	runStatusErrored runStatus = "errored"
)

// turn contains all mutable state belonging to one logical turn, including the
// executor that owns its DeepAgent graph.
// DeepAgentThread.mu protects status, pending input, and consumed input
// snapshots; the run's channels coordinate execution and event draining.
type turn struct {
	turnID string
	input  *Message
	opts   *ResumeTurnOptions
	runner *TurnRunner

	status   runStatus
	pending  []*schema.Message
	consumed []*schema.Message
	// consumedInputsMeta has the same logical order as consumed. Nil entries
	// are kept internally so later non-nil metadata still aligns by index.
	consumedInputsMeta []any

	events        chan Event
	eventsDrained chan struct{}
	done          chan struct{}
	runErr        error
}

func newTurn(turnID string, input *Message, inputMeta any, opts *ResumeTurnOptions) *turn {
	run := &turn{
		turnID:        turnID,
		input:         graph.CopyMessage(input),
		opts:          copyResumeTurnOptions(opts),
		status:        runStatusRunning,
		events:        make(chan Event, defaultRunEventBufferSize),
		eventsDrained: make(chan struct{}),
		done:          make(chan struct{}),
	}
	if input != nil {
		run.consumed = append(run.consumed, graph.CopyMessage(input))
		run.consumedInputsMeta = append(run.consumedInputsMeta, inputMeta)
	}
	return run
}

func (r *turn) isActive() bool {
	return r.status == runStatusRunning || r.status == runStatusEnding
}

func (r *turn) enqueueInput(input *Message, inputMeta any) error {
	if input == nil {
		return ErrInvalidOp
	}
	cloned := graph.CopyMessage(input)
	if r.status != runStatusRunning {
		return ErrRunInputClosed
	}
	r.pending = append(r.pending, cloned)
	r.consumed = append(r.consumed, graph.CopyMessage(cloned))
	r.consumedInputsMeta = append(r.consumedInputsMeta, inputMeta)
	return nil
}

func (r *turn) drainInput() []*schema.Message {
	if len(r.pending) == 0 {
		return nil
	}
	out := make([]*schema.Message, len(r.pending))
	copy(out, r.pending)
	r.pending = nil
	return out
}

func (r *turn) commitEndIfNoPending() bool {
	if len(r.pending) > 0 {
		return false
	}
	if r.status == runStatusRunning {
		r.status = runStatusEnding
	}
	return true
}

func (r *turn) complete(err error) {
	r.runErr = err
	if err != nil {
		r.status = runStatusErrored
	} else {
		r.status = runStatusEnded
	}

	close(r.done)
}

func (r *turn) err() error {
	return r.runErr
}

func (r *turn) turnRunOptions() *TurnRunOptions {
	if r.opts == nil {
		return nil
	}
	return &TurnRunOptions{
		CheckpointID:        r.opts.CheckpointID,
		WriteToCheckpointID: r.opts.WriteToCheckpointID,
		ForceNewRun:         r.opts.ForceNewRun,
		ResumeInterruptIDs:  r.opts.ResumeInterruptIDs,
		ResumeData:          r.opts.ResumeData,
	}
}
