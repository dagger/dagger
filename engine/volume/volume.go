package volume

import "context"

type Volume[K comparable, V any] interface {
	GetOrInigialize(ctx context.Context)
}

type VolumeKey[K comparable] struct {
	ResultKey K
}
