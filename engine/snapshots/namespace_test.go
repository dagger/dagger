package snapshots

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// WithNamespace leaves a context that already carries the namespace as it
// is, and attaches it to any other, so the in-process stores see it.
func TestWithNamespace(t *testing.T) {
	attached := namespaces.WithNamespace(context.Background(), "dagger")
	for name, ctx := range map[string]context.Context{
		"attached":          attached,
		"incoming":          metadata.NewIncomingContext(context.Background(), metadata.Pairs(namespaces.GRPCHeader, "dagger")),
		"replaced outgoing": metadata.NewOutgoingContext(attached, metadata.Pairs("other-header", "x")),
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, ctx, WithNamespace(ctx, "dagger"))
		})
	}

	for name, ctx := range map[string]context.Context{
		"bare":  context.Background(),
		"other": namespaces.WithNamespace(context.Background(), "other"),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := namespaces.NamespaceRequired(WithNamespace(ctx, "dagger"))
			require.NoError(t, err)
			require.Equal(t, "dagger", got)
		})
	}
}
