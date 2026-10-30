package server

import (
	"context"
	"testing"

	ctdsnapshot "github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	bksnapshots "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// headerRecordingSnapshotter stands in for the gRPC proxy: it records the
// namespace each call's headers would give a containerd server, which reads
// the header's first value.
type headerRecordingSnapshotter struct {
	ctdsnapshot.Snapshotter
	headers []string
}

func (s *headerRecordingSnapshotter) Stat(ctx context.Context, key string) (ctdsnapshot.Info, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	if v := md.Get(namespaces.GRPCHeader); len(v) > 0 {
		s.headers = append(s.headers, v[0])
	}
	return ctdsnapshot.Info{Name: key}, nil
}

// A proxied snapshotter gets the namespace on its gRPC headers whenever the
// call's context carries it, including where the engine's WithNamespace left
// the context as it was because it already had the namespace without them.
func TestNamespaceHeaderSnapshotter(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"bare":              bksnapshots.WithNamespace(context.Background(), "dagger"),
		"replaced outgoing": bksnapshots.WithNamespace(metadata.NewOutgoingContext(namespaces.WithNamespace(context.Background(), "dagger"), metadata.Pairs("other-header", "x")), "dagger"),
		"incoming":          bksnapshots.WithNamespace(metadata.NewIncomingContext(context.Background(), metadata.Pairs(namespaces.GRPCHeader, "dagger")), "dagger"),
	} {
		t.Run(name, func(t *testing.T) {
			rec := &headerRecordingSnapshotter{}
			_, err := (&namespaceHeaderSnapshotter{Snapshotter: rec}).Stat(ctx, "key")
			require.NoError(t, err)
			require.Equal(t, []string{"dagger"}, rec.headers)
		})
	}
}
