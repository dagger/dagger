package snapshots

import (
	"context"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/ttrpc"
	"google.golang.org/grpc/metadata"
)

// WithNamespace is namespaces.WithNamespace, unless ctx already carries ns
// everywhere WithNamespace puts it: the context value, which in-process
// stores read, and the outgoing gRPC and ttrpc headers, which proxied ones
// get. Attaching copies the context's gRPC and ttrpc metadata, which every
// call through the namespace wrappers paid again even when its caller had
// attached the same namespace already.
func WithNamespace(ctx context.Context, ns string) context.Context {
	if cur, ok := namespaces.Namespace(ctx); !ok || cur != ns {
		return namespaces.WithNamespace(ctx, ns)
	}
	if cur, ok := ttrpc.GetMetadataValue(ctx, namespaces.TTRPCHeader); !ok || cur != ns {
		return namespaces.WithNamespace(ctx, ns)
	}
	if md, ok := metadata.FromOutgoingContext(ctx); !ok || len(md[namespaces.GRPCHeader]) == 0 || md[namespaces.GRPCHeader][0] != ns {
		return namespaces.WithNamespace(ctx, ns)
	}
	return ctx
}
