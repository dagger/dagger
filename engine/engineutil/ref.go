package engineutil

import (
	"context"
	"fmt"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	snapshotutil "github.com/dagger/dagger/engine/snapshots/util"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
)

const (
	// Exec errors will only include the last this number of bytes of output.
	MaxExecErrorOutputBytes = 100 * 1024

	// TruncationMessage is the message that will be prepended to truncated output.
	TruncationMessage = "[omitting %d bytes]..."

	// MaxFileContentsChunkSize sets the maximum chunk size for ReadFile calls
	// Equals around 95% of the max message size (4MB) in
	// order to keep space for any Protocol Buffers overhead:
	MaxFileContentsChunkSize = 3984588

	// MaxFileContentsSize sets the limit of the maximum file size
	// that can be retrieved using File.Contents, currently set to 128MB:
	MaxFileContentsSize = 128 << 20

	// MetaMountDestPath is the special path that the shim writes metadata to.
	MetaMountDestPath           = "/.dagger_meta_mount"
	MetaMountExitCodePath       = "exitCode"
	MetaMountStdoutPath         = "stdout"
	MetaMountStderrPath         = "stderr"
	MetaMountCombinedOutputPath = "combinedOutput"
)

func ReadSnapshotPath(ctx context.Context, c *Client, mntable bkcache.MountableRef, filePath string, limit int) ([]byte, error) {
	ctx = withOutgoingContext(ctx)
	stat, err := snapshotutil.StatFile(ctx, mntable, filePath)
	if err != nil {
		// TODO: would be better to verify this is a "not exists" error, return err if not
		bklog.G(ctx).Debugf("ReadSnapshotPath: failed to stat file: %v", err)
		return nil, nil
	}

	req := snapshotutil.ReadRequest{
		Filename: filePath,
		Range: &snapshotutil.FileRange{
			Length: int(stat.Size_),
		},
	}

	if limit != -1 && req.Range.Length > limit {
		req.Range.Offset = int(stat.Size_) - limit
		req.Range.Length = limit
	}
	contents, err := snapshotutil.ReadFile(ctx, mntable, req)
	if err != nil {
		return nil, fmt.Errorf("failed to read %q: %w", filePath, err)
	}
	if limit != -1 && len(contents) >= limit {
		truncMsg := fmt.Sprintf(TruncationMessage, int(stat.Size_)-limit)
		copy(contents, truncMsg)
	}
	return contents, nil
}
