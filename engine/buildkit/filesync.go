package buildkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/dagger/dagger/internal/buildkit/session/filesync"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/internal/fsutil"
	fsutiltypes "github.com/dagger/dagger/internal/fsutil/types"
	telemetry "github.com/dagger/otel-go"
	"google.golang.org/grpc/metadata"

	"github.com/dagger/dagger/engine"
)

// injectTraceContext adds W3C trace context to the gRPC outgoing metadata,
// so that client-side session attachables can create child spans.
func injectTraceContext(ctx context.Context) context.Context {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.MD{}
	} else {
		md = md.Copy()
	}
	telemetry.Propagator.Inject(ctx, metadataCarrier(md))
	return metadata.NewOutgoingContext(ctx, md)
}

// metadataCarrier adapts gRPC metadata.MD to propagation.TextMapCarrier.
// Unlike propagation.HeaderCarrier (which wraps http.Header and title-cases
// keys), this keeps keys lowercase as required by gRPC metadata.
type metadataCarrier metadata.MD

func (mc metadataCarrier) Get(key string) string {
	vals := metadata.MD(mc).Get(key)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

func (mc metadataCarrier) Set(key, value string) {
	metadata.MD(mc).Set(key, value)
}

func (mc metadataCarrier) Keys() []string {
	keys := make([]string, 0, len(mc))
	for k := range mc {
		keys = append(keys, k)
	}
	return keys
}

func (c *Client) diffcopy(ctx context.Context, opts engine.LocalImportOpts, msg any) error {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return err
	}
	defer cancel(errors.New("diff copy done"))

	ctx = opts.AppendToOutgoingContext(ctx)

	// Propagate OTel trace context to the client via gRPC metadata
	ctx = injectTraceContext(ctx)

	clientCaller, err := c.GetSessionCaller(ctx, false)
	if err != nil {
		return fmt.Errorf("failed to get requester session: %w", err)
	}
	diffCopyClient, err := filesync.NewFileSyncClient(clientCaller.Conn()).DiffCopy(ctx)
	if err != nil {
		return fmt.Errorf("failed to create diff copy client: %w", err)
	}
	defer diffCopyClient.CloseSend()

	err = diffCopyClient.RecvMsg(msg)
	if err != nil {
		return fmt.Errorf("failed to receive file bytes message: %w", err)
	}
	return err
}

func (c *Client) ReadCallerHostFile(ctx context.Context, path string) ([]byte, error) {
	msg := filesync.BytesMessage{}
	err := c.diffcopy(ctx, engine.LocalImportOpts{
		Path:               path,
		ReadSingleFileOnly: true,
		MaxFileSize:        MaxFileContentsChunkSize,
	}, &msg)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return msg.Data, nil
}

// Return the absolute path of the given path as calculated by the caller's host using OS-specific rules.
func (c *Client) AbsPath(ctx context.Context, path string) (string, error) {
	msg := fsutiltypes.Stat{}
	err := c.diffcopy(ctx, engine.LocalImportOpts{
		Path:           path,
		GetAbsPathOnly: true,
	}, &msg)
	if err != nil {
		return "", fmt.Errorf("failed to stat path: %w", err)
	}
	return msg.Path, nil
}

func (c *Client) StatCallerHostPath(ctx context.Context, path string, returnAbsPath bool) (*fsutiltypes.Stat, error) {
	return c.StatCallerHostPathFollow(ctx, path, returnAbsPath, false)
}

func (c *Client) StatCallerHostPathFollow(ctx context.Context, path string, returnAbsPath bool, followSymlinks bool) (*fsutiltypes.Stat, error) {
	msg := fsutiltypes.Stat{}
	err := c.diffcopy(ctx, engine.LocalImportOpts{
		Path:               path,
		StatPathOnly:       true,
		StatReturnAbsPath:  returnAbsPath,
		StatFollowSymlinks: followSymlinks,
	}, &msg)
	if err != nil {
		return nil, fmt.Errorf("failed to stat path: %w", err)
	}
	return &msg, nil
}

func (c *Client) SearchCallerHostPath(ctx context.Context, dir string, opts *engine.LocalSearchOpts) ([]engine.LocalSearchResult, error) {
	msg := filesync.BytesMessage{}
	err := c.diffcopy(ctx, engine.LocalImportOpts{
		Path:       dir,
		SearchOpts: opts,
	}, &msg)
	if err != nil {
		return nil, fmt.Errorf("failed to search path: %w", err)
	}
	var results []engine.LocalSearchResult
	if err := json.Unmarshal(msg.Data, &results); err != nil {
		return nil, fmt.Errorf("failed to unmarshal search results: %w", err)
	}
	return results, nil
}

func (c *Client) GlobCallerHostPath(ctx context.Context, dir string, pattern string) ([]string, error) {
	msg := filesync.BytesMessage{}
	err := c.diffcopy(ctx, engine.LocalImportOpts{
		Path:        dir,
		GlobPattern: pattern,
	}, &msg)
	if err != nil {
		return nil, fmt.Errorf("failed to glob path: %w", err)
	}
	var matches []string
	if err := json.Unmarshal(msg.Data, &matches); err != nil {
		return nil, fmt.Errorf("failed to unmarshal glob results: %w", err)
	}
	return matches, nil
}

// GitBranch detects the current git branch in the given directory on the client host.
func (c *Client) GitBranch(ctx context.Context, repoDir string) (string, error) {
	msg := filesync.BytesMessage{}
	err := c.diffcopy(ctx, engine.LocalImportOpts{
		Path:            repoDir,
		GitBranchDetect: true,
	}, &msg)
	if err != nil {
		return "", fmt.Errorf("failed to detect git branch: %w", err)
	}
	return string(msg.Data), nil
}

// GitWorktreeAdd creates a git worktree on the client host.
func (c *Client) GitWorktreeAdd(ctx context.Context, repoDir, branch, worktreePath, base string) (string, error) {
	msg := filesync.BytesMessage{}
	err := c.diffcopy(ctx, engine.LocalImportOpts{
		Path: repoDir,
		GitWorktreeAdd: &engine.GitWorktreeAddOpts{
			Branch:       branch,
			WorktreePath: worktreePath,
			Base:         base,
		},
	}, &msg)
	if err != nil {
		return "", fmt.Errorf("failed to add git worktree: %w", err)
	}
	return string(msg.Data), nil
}

// GitStage stages the given paths in the git index and merges changes
// into the working tree. The tempDir contains the exported changeset
// files. For added files, they are copied to the worktree. For modified
// files, go-git writes the blob directly to the index and git merge-file
// 3-way merges the change into the working tree (preserving user edits).
func (c *Client) GitStage(
	ctx context.Context,
	worktreeDir string,
	tempDir string,
	added, modified, removed []string,
	force bool,
) (bool, error) {
	msg := filesync.BytesMessage{}
	err := c.diffcopy(ctx, engine.LocalImportOpts{
		Path: path.Clean(worktreeDir),
		GitStage: &engine.GitStageOpts{
			Added:    added,
			Modified: modified,
			Removed:  removed,
			TempDir:  tempDir,
			Force:    force,
		},
	}, &msg)
	if err != nil {
		return false, fmt.Errorf("git stage: %w", err)
	}
	return string(msg.Data) == "true", nil
}

// GitCommit commits whatever is currently staged and returns the commit hash.
func (c *Client) GitCommit(ctx context.Context, worktreeDir string, message string) (string, error) {
	msg := filesync.BytesMessage{}
	err := c.diffcopy(ctx, engine.LocalImportOpts{
		Path: path.Clean(worktreeDir),
		GitCommit: &engine.GitCommitOpts{
			Message: message,
		},
	}, &msg)
	if err != nil {
		return "", fmt.Errorf("git commit: %w", err)
	}
	return string(msg.Data), nil
}

func (c *Client) LocalDirExport(
	ctx context.Context,
	srcPath string,
	destPath string,
	merge bool,
	removePaths []string,
) error {
	return c.localDirExportWithOpts(ctx, srcPath, engine.LocalExportOpts{
		Path:        path.Clean(destPath),
		Merge:       merge,
		RemovePaths: removePaths,
	})
}

func (c *Client) localDirExportWithOpts(
	ctx context.Context,
	srcPath string,
	opts engine.LocalExportOpts,
) (rerr error) {
	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("export_path", opts.Path))
	bklog.G(ctx).Debug("exporting local dir")

	defer func() {
		lg := bklog.G(ctx)
		if rerr != nil {
			lg = lg.WithError(rerr)
		}
		lg.Trace("finished exporting local dir")
	}()

	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return err
	}
	defer cancel(errors.New("local dir export done"))

	outputFS, err := fsutil.NewFS(srcPath)
	if err != nil {
		return err
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}

	caller, err := c.SessionManager.Get(ctx, clientMetadata.ClientID, false)
	if err != nil {
		return err
	}

	ctx = opts.AppendToOutgoingContext(ctx)

	if err := filesync.CopyToCaller(ctx, outputFS, 0, caller, nil); err != nil {
		return err
	}

	return nil
}

func (c *Client) LocalFileExport(
	ctx context.Context,
	srcPath string,
	filePath string,
	destPath string,
	allowParentDirPath bool,
) (rerr error) {
	ctx = bklog.WithLogger(ctx, bklog.G(ctx).
		WithField("export_path", destPath).
		WithField("file_path", filePath).
		WithField("allow_parent_dir_path", allowParentDirPath),
	)
	bklog.G(ctx).Debug("exporting local file")
	defer func() {
		lg := bklog.G(ctx)
		if rerr != nil {
			lg = lg.WithError(rerr)
		}
		lg.Trace("finished exporting local file")
	}()

	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return err
	}
	defer cancel(errors.New("local file export done"))

	destPath = path.Clean(destPath)

	file, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	ctx = engine.LocalExportOpts{
		Path:               destPath,
		IsFileStream:       true,
		FileOriginalName:   filepath.Base(filePath),
		AllowParentDirPath: allowParentDirPath,
		FileMode:           stat.Mode().Perm(),
	}.AppendToOutgoingContext(ctx)

	clientCaller, err := c.GetSessionCaller(ctx, false)
	if err != nil {
		return fmt.Errorf("failed to get requester session: %w", err)
	}
	diffCopyClient, err := filesync.NewFileSendClient(clientCaller.Conn()).DiffCopy(ctx)
	if err != nil {
		return fmt.Errorf("failed to create diff copy client: %w", err)
	}
	defer diffCopyClient.CloseSend()

	fileSizeLeft := stat.Size()
	chunkSize := int64(MaxFileContentsChunkSize)
	for fileSizeLeft > 0 {
		buf := new(bytes.Buffer) // TODO: more efficient to use bufio.Writer, reuse buffers, sync.Pool, etc.
		n, err := io.CopyN(buf, file, chunkSize)
		if errors.Is(err, io.EOF) {
			err = nil
		}
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}
		fileSizeLeft -= n
		err = diffCopyClient.SendMsg(&filesync.BytesMessage{Data: buf.Bytes()})
		if errors.Is(err, io.EOF) {
			err := diffCopyClient.RecvMsg(struct{}{})
			if err != nil {
				return fmt.Errorf("diff copy client error: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("failed to send file chunk: %w", err)
		}
	}
	if err := diffCopyClient.CloseSend(); err != nil {
		return fmt.Errorf("failed to close send: %w", err)
	}
	// wait for receiver to finish
	var msg filesync.BytesMessage
	if err := diffCopyClient.RecvMsg(&msg); err != io.EOF {
		return fmt.Errorf("unexpected closing recv msg: %w", err)
	}
	return nil
}

// IOReaderExport exports the contents of an io.Reader to the caller's local fs as a file
// TODO: de-dupe this with the above method to extent possible
func (c *Client) IOReaderExport(ctx context.Context, r io.Reader, destPath string, destMode os.FileMode) (rerr error) {
	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("export_path", destPath))
	bklog.G(ctx).Debug("exporting bytes")
	defer func() {
		lg := bklog.G(ctx)
		if rerr != nil {
			lg = lg.WithError(rerr)
		}
		lg.Trace("finished exporting bytes")
	}()

	ctx = engine.LocalExportOpts{
		Path:             destPath,
		IsFileStream:     true,
		FileOriginalName: filepath.Base(destPath),
		FileMode:         destMode,
	}.AppendToOutgoingContext(ctx)

	clientCaller, err := c.GetSessionCaller(ctx, false)
	if err != nil {
		return fmt.Errorf("failed to get requester session: %w", err)
	}
	diffCopyClient, err := filesync.NewFileSendClient(clientCaller.Conn()).DiffCopy(ctx)
	if err != nil {
		return fmt.Errorf("failed to create diff copy client: %w", err)
	}
	defer diffCopyClient.CloseSend()

	chunkSize := int64(MaxFileContentsChunkSize)
	keepGoing := true
	for keepGoing {
		buf := new(bytes.Buffer) // TODO: more efficient to use bufio.Writer, reuse buffers, sync.Pool, etc.
		_, err := io.CopyN(buf, r, chunkSize)
		if errors.Is(err, io.EOF) {
			keepGoing = false
			err = nil
		}
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}
		err = diffCopyClient.SendMsg(&filesync.BytesMessage{Data: buf.Bytes()})
		if errors.Is(err, io.EOF) {
			err := diffCopyClient.RecvMsg(struct{}{})
			if err != nil {
				return fmt.Errorf("diff copy client error: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("failed to send file chunk: %w", err)
		}
	}
	if err := diffCopyClient.CloseSend(); err != nil {
		return fmt.Errorf("failed to close send: %w", err)
	}
	// wait for receiver to finish
	var msg filesync.BytesMessage
	if err := diffCopyClient.RecvMsg(&msg); err != io.EOF {
		return fmt.Errorf("unexpected closing recv msg: %w", err)
	}
	return nil
}
