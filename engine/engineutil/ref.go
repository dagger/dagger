package engineutil

import (
	"context"
	"fmt"
	"os"

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

	// FunctionErrorPathEnv is part of the module entrypoint contract: the
	// engine sets it, on a module entrypoint's module process only (see
	// core.ModuleCallDigestEnv), to a writable file path. A module process
	// whose function fails writes the function's error there and exits
	// non-zero; the call then fails with that error as its own. The file holds
	// {"message": "...", "values": {...}}, {"id": "<Error ID>"}, or plain
	// text, the whole of which is the message; an object with any other field
	// is plain text too (see core.FunctionErrorReport).
	// The engine reads a regular file there of at most MaxFunctionErrorBytes,
	// and nothing else; without one, the call fails with the process's exec
	// error. A process that finds the variable unset runs on an engine
	// without the contract.
	FunctionErrorPathEnv = "DAGGER_FUNCTION_ERROR_PATH"
	// FunctionErrorMountPath is the directory the module process's
	// FunctionErrorPathEnv file is in, bound to MetaMountFunctionErrorDir of
	// the exec's meta mount, so the file lands beside the exec's output and
	// stays out of its filesystem.
	FunctionErrorMountPath = "/.dagger_function_error"
	// FunctionErrorFileName is the FunctionErrorPathEnv file's name in
	// FunctionErrorMountPath.
	FunctionErrorFileName = "error"
	// MetaMountFunctionErrorDir is the directory of the meta mount that
	// FunctionErrorMountPath binds.
	MetaMountFunctionErrorDir = "functionError"
	// MaxFunctionErrorBytes is the largest function error file the engine
	// reads.
	MaxFunctionErrorBytes = 1 << 20
)

// ReadSnapshotSmallFile reads the regular file at filePath, and nothing when
// there's none there or it is over limit bytes: a path that a process can
// make anything, such as a named pipe that would block the read, is read only
// when it is a plain file.
func ReadSnapshotSmallFile(ctx context.Context, mntable bkcache.MountableRef, filePath string, limit int) ([]byte, error) {
	ctx = withOutgoingContext(ctx)
	stat, err := snapshotutil.StatFile(ctx, mntable, filePath)
	if err != nil || !os.FileMode(stat.Mode).IsRegular() || stat.Size_ > int64(limit) {
		return nil, nil
	}
	contents, err := snapshotutil.ReadFile(ctx, mntable, snapshotutil.ReadRequest{
		Filename: filePath,
		Range:    &snapshotutil.FileRange{Length: int(stat.Size_)},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read %q: %w", filePath, err)
	}
	return contents, nil
}

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
