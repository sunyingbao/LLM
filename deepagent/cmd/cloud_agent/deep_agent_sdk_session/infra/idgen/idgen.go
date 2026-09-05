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

type Generator interface {
	SessionID(ctx context.Context) (int64, error)
}

type LocalGenerator struct {
	last int64
}

type ntClient interface {
	Get(context.Context) (uint64, error)
}

type NTGenerator struct {
	client ntClient
}

func NewGenerator(namespace string) (Generator, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return NewLocalGenerator(), nil
	}
	return NewNTGenerator(namespace)
}

func NewNTGenerator(namespace string) (*NTGenerator, error) {
	client, err := idgenerator.NewNtIdGeneratorBuilder().
		WithNamespace(namespace).
		Build()
	if err != nil {
		return nil, fmt.Errorf("build nt id generator namespace=%s: %w", namespace, err)
	}
	return &NTGenerator{client: client}, nil
}

func NewLocalGenerator() *LocalGenerator {
	return &LocalGenerator{}
}

func (g *LocalGenerator) SessionID(context.Context) (int64, error) {
	return g.next(), nil
}

func (g *NTGenerator) SessionID(ctx context.Context) (int64, error) {
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

func (g *LocalGenerator) next() int64 {
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
