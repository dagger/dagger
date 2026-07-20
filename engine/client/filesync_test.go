package client

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/buildkit/session/filesync"
	fstypes "github.com/dagger/dagger/internal/fsutil/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestGlobHostPathCancellation(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "match.txt"), []byte("x"), 0o600))

	opts := engine.LocalImportOpts{Path: root, GlobPattern: "**"}
	ctx := metadata.NewIncomingContext(context.Background(), opts.ToGRPCMD())
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	stream := &canceledDiffCopyStream{ctx: ctx}

	err := (FilesyncSource{}).DiffCopy(stream)
	require.ErrorIs(t, err, context.Canceled)
}

type canceledDiffCopyStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *canceledDiffCopyStream) Context() context.Context     { return s.ctx }
func (*canceledDiffCopyStream) Send(*fstypes.Packet) error     { return nil }
func (*canceledDiffCopyStream) Recv() (*fstypes.Packet, error) { return nil, io.EOF }
func (*canceledDiffCopyStream) SendMsg(any) error              { return nil }

var _ filesync.FileSync_DiffCopyServer = (*canceledDiffCopyStream)(nil)
