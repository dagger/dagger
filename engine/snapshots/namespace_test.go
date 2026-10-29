package snapshots

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/ttrpc"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// WithNamespace leaves a context that carries the namespace everywhere as it
// is, and attaches it to any other, so in-process and proxied stores both
// see it.
func TestWithNamespace(t *testing.T) {
	requireNamespaced := func(t *testing.T, ctx context.Context, ns string) {
		t.Helper()
		got, ok := namespaces.Namespace(ctx)
		require.True(t, ok)
		require.Equal(t, ns, got)
		got, ok = ttrpc.GetMetadataValue(ctx, namespaces.TTRPCHeader)
		require.True(t, ok)
		require.Equal(t, ns, got)
		md, ok := metadata.FromOutgoingContext(ctx)
		require.True(t, ok)
		require.Equal(t, ns, md[namespaces.GRPCHeader][0])
	}

	attached := namespaces.WithNamespace(context.Background(), "dagger")
	require.Equal(t, attached, WithNamespace(attached, "dagger"), "already namespaced")

	for name, ctx := range map[string]context.Context{
		"bare":              context.Background(),
		"other":             namespaces.WithNamespace(context.Background(), "other"),
		"incoming":          metadata.NewIncomingContext(context.Background(), metadata.Pairs(namespaces.GRPCHeader, "dagger")),
		"replaced outgoing": metadata.NewOutgoingContext(attached, metadata.Pairs("other-header", "x")),
		"replaced ttrpc":    ttrpc.WithMetadata(attached, ttrpc.MD{}),
	} {
		t.Run(name, func(t *testing.T) {
			got := WithNamespace(ctx, "dagger")
			require.NotEqual(t, ctx, got)
			requireNamespaced(t, got, "dagger")
		})
	}
}
