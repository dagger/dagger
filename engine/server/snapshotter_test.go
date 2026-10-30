package server

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	snapshotsapi "github.com/containerd/containerd/api/services/snapshots/v1"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	bksnapshots "github.com/dagger/dagger/engine/snapshots"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// namespaceRecordingSnapshots is a proxied snapshotter's server: it records
// the namespace each Stat's headers give it, which containerd reads from the
// header's first value.
type namespaceRecordingSnapshots struct {
	snapshotsapi.UnimplementedSnapshotsServer
	mu         sync.Mutex
	namespaces []string
}

func (s *namespaceRecordingSnapshots) Stat(ctx context.Context, req *snapshotsapi.StatSnapshotRequest) (*snapshotsapi.StatSnapshotResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	ns := ""
	if v := md.Get(namespaces.GRPCHeader); len(v) > 0 {
		ns = v[0]
	}
	s.mu.Lock()
	s.namespaces = append(s.namespaces, ns)
	s.mu.Unlock()
	return &snapshotsapi.StatSnapshotResponse{Info: &snapshotsapi.Info{Name: req.Key}}, nil
}

// A proxied snapshotter gets the namespace on its gRPC headers whenever the
// call's context carries it, including where the engine's WithNamespace left
// the context as it was because it already had the namespace without them.
func TestProxySnapshotterNamespace(t *testing.T) {
	// A unix socket path must stay short, so not under t.TempDir.
	dir, err := os.MkdirTemp("", "proxysn")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "sock")
	l, err := net.Listen("unix", sock)
	require.NoError(t, err)
	server := grpc.NewServer()
	rec := &namespaceRecordingSnapshots{}
	snapshotsapi.RegisterSnapshotsServer(server, rec)
	go func() { _ = server.Serve(l) }()
	t.Cleanup(server.Stop)

	sn, _, err := newSnapshotter(t.TempDir(), bkconfig.OCIConfig{Snapshotter: "proxied", ProxySnapshotterPath: sock}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sn.Close() })

	for name, ctx := range map[string]context.Context{
		"bare":              bksnapshots.WithNamespace(context.Background(), "dagger"),
		"replaced outgoing": bksnapshots.WithNamespace(metadata.NewOutgoingContext(namespaces.WithNamespace(context.Background(), "dagger"), metadata.Pairs("other-header", "x")), "dagger"),
		"incoming":          bksnapshots.WithNamespace(metadata.NewIncomingContext(context.Background(), metadata.Pairs(namespaces.GRPCHeader, "dagger")), "dagger"),
	} {
		t.Run(name, func(t *testing.T) {
			rec.mu.Lock()
			rec.namespaces = nil
			rec.mu.Unlock()
			_, err := sn.Stat(ctx, "key")
			require.NoError(t, err)
			rec.mu.Lock()
			defer rec.mu.Unlock()
			require.Equal(t, []string{"dagger"}, rec.namespaces)
		})
	}
}
