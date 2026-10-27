package filesync

import (
	"context"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/client"
	sessionfilesync "github.com/dagger/dagger/internal/buildkit/session/filesync"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestGitignoreCapabilitiesThroughProxy(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "keep.txt"), []byte("keep"), 0600))
	for _, oldClient := range []bool{false, true} {
		name := "current client"
		if oldClient {
			name = "older client"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var source sessionfilesync.FileSyncServer = client.FilesyncSource{}
			if oldClient {
				source = &filesyncWithoutCapabilities{}
			}
			upstream := serveFilesync(t, source)
			proxy := serveFilesync(t, client.FilesyncSourceProxy{Client: sessionfilesync.NewFileSyncClient(upstream)})
			remote := newRemoteFS(proxy, root, nil, nil, nil, true)
			remote.gitIgnoreOverrides = map[string]string{".gitignore": "ignored.txt\n"}
			var paths []string
			err := remote.Walk(ctx, "", func(path string, _ fs.DirEntry, err error) error {
				paths = append(paths, path)
				return err
			})
			if oldClient {
				require.ErrorContains(t, err, "client does not support overlay .gitignore rules; upgrade the Dagger CLI")
			} else {
				require.NoError(t, err)
				require.Contains(t, paths, "keep.txt")
			}
		})
	}
}

func serveFilesync(t *testing.T, source sessionfilesync.FileSyncServer) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	sessionfilesync.RegisterFileSyncServer(server, source)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///filesync-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// An older client completes a stream without acknowledging ignore overrides.
type filesyncWithoutCapabilities struct {
	sessionfilesync.UnimplementedFileSyncServer
}

func (filesyncWithoutCapabilities) DiffCopy(sessionfilesync.FileSync_DiffCopyServer) error {
	return nil
}
