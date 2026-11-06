package snapshots

import (
	"context"

	"github.com/containerd/containerd/v2/pkg/namespaces"
)

// WithNamespace is namespaces.WithNamespace, unless ctx already carries ns
// where namespaces.Namespace finds it, which is what the engine's in-process
// containerd stores read. The outgoing gRPC and ttrpc headers that
// namespaces.WithNamespace also attaches matter only to a proxied
// snapshotter, whose client attaches them itself (see engine/server's
// newSnapshotter). Checking the outgoing headers here would copy the
// context's gRPC metadata on every call through the namespace wrappers.
func WithNamespace(ctx context.Context, ns string) context.Context {
	if cur, ok := namespaces.Namespace(ctx); ok && cur == ns {
		return ctx
	}
	return namespaces.WithNamespace(ctx, ns)
}
