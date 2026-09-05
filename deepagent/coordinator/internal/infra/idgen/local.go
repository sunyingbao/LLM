package idgen

import (
	"context"
	"sync/atomic"
	"time"
)

type LocalGenerator struct {
	counter atomic.Int64
}

func NewLocalGenerator() *LocalGenerator {
	gen := &LocalGenerator{}
	gen.counter.Store(time.Now().UnixNano())
	return gen
}

func (g *LocalGenerator) NextID(context.Context) (int64, error) {
	return g.counter.Add(1), nil
}
