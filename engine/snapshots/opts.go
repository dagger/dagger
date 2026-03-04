package snapshots

import (
	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/internal/buildkit/session"
	digest "github.com/opencontainers/go-digest"
)

type DescHandler struct {
	Provider       func(session.Group) content.Provider
	SnapshotLabels map[string]string
	Annotations    map[string]string
	Ref            string // string representation of desc origin, can be used as a sync key
}

type DescHandlers map[digest.Digest]*DescHandler

func descHandlersOf(opts ...RefOption) DescHandlers {
	for _, opt := range opts {
		if opt, ok := opt.(DescHandlers); ok {
			return opt
		}
	}
	return nil
}
