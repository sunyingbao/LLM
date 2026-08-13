package idgen

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"code.byted.org/gopkg/idgenerator/v2"
)

// LocalIDGenerator is intended for local development and dogfood wiring. It is
// monotonic inside one process but is not a distributed id generator.
type LocalIDGenerator struct {
	last int64
}

type Generator interface {
	MessageID(ctx context.Context, threadID string, turnID string) int64
	NextID(ctx context.Context) (int64, error)
	EventID(ctx context.Context, threadID string, turnID string) string
}

type ntClient interface {
	Get(context.Context) (uint64, error)
}

type NTIDGenerator struct {
	client ntClient
}

func NewGenerator(namespace string) (Generator, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return NewLocalIDGenerator(), nil
	}
	return NewNTIDGenerator(namespace)
}

func NewNTIDGenerator(namespace string) (*NTIDGenerator, error) {
	client, err := idgenerator.NewNtIdGeneratorBuilder().
		WithNamespace(namespace).
		Build()
	if err != nil {
		return nil, fmt.Errorf("build nt id generator namespace=%s: %w", namespace, err)
	}
	return &NTIDGenerator{client: client}, nil
}

func NewLocalIDGenerator() *LocalIDGenerator {
	return &LocalIDGenerator{}
}

func (g *LocalIDGenerator) MessageID(_ context.Context, _ string, _ string) int64 {
	return g.nextInt64()
}

func (g *LocalIDGenerator) NextID(_ context.Context) (int64, error) {
	return g.nextInt64(), nil
}

func (g *LocalIDGenerator) EventID(_ context.Context, threadID string, turnID string) string {
	return fmt.Sprintf("evt_%s_%s_%d", threadID, turnID, g.nextInt64())
}

func (g *NTIDGenerator) MessageID(ctx context.Context, _ string, _ string) int64 {
	id, err := g.NextID(ctx)
	if err != nil {
		panic(fmt.Sprintf("generate nt message id: %v", err))
	}
	return id
}

func (g *NTIDGenerator) NextID(ctx context.Context) (int64, error) {
	if g == nil || g.client == nil {
		return 0, fmt.Errorf("nt id generator is not initialized")
	}
	id, err := g.client.Get(ctx)
	if err != nil {
		return 0, err
	}
	if id > math.MaxInt64 {
		return 0, fmt.Errorf("nt id %d overflows int64", id)
	}
	return int64(id), nil
}

func (g *NTIDGenerator) EventID(ctx context.Context, threadID string, turnID string) string {
	id, err := g.NextID(ctx)
	if err != nil {
		panic(fmt.Sprintf("generate nt event id: %v", err))
	}
	return fmt.Sprintf("evt_%s_%s_%d", threadID, turnID, id)
}

func (g *LocalIDGenerator) nextInt64() int64 {
	if g == nil {
		return time.Now().UnixNano()
	}
	for {
		next := time.Now().UnixMicro()*1000 + int64(os.Getpid()%1000)
		last := atomic.LoadInt64(&g.last)
		if next <= last {
			next = last + 1
		}
		if atomic.CompareAndSwapInt64(&g.last, last, next) {
			return next
		}
	}
}
