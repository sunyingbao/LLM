package idgen

import "context"

type Generator interface {
	NextID(context.Context) (int64, error)
}
