package engineutil

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
	"github.com/dagger/dagger/internal/fsutil"
	fsutiltypes "github.com/dagger/dagger/internal/fsutil/types"
	telemetry "github.com/dagger/otel-go"
	"google.golang.org/grpc/metadata"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/util/tracing"
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

	clientCaller, err := c.GetSessionCaller(ctx)
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
	msg := fsutiltypes.Stat{}
	err := c.diffcopy(ctx, engine.LocalImportOpts{
		Path:              path,
		StatPathOnly:      true,
		StatReturnAbsPath: returnAbsPath,
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

func (c *Client) LocalDirExport(
	ctx context.Context,
	srcPath string,
	destPath string,
	merge bool,
	removePaths []string,
) (rerr error) {
	ctx = slog.WithLogger(ctx, slog.FromContext(ctx).With("export_path", destPath))
	slog.DebugContext(ctx, "exporting local dir")
	defer func() {
		slog.TraceContext(ctx, "finished exporting local dir", "err", rerr)
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

	caller, err := c.GetSessionCaller(ctx)
	if err != nil {
		return fmt.Errorf("failed to get requester session attachables: %w", err)
	}

	destPath = path.Clean(destPath)
	method := "/moby.filesync.v1.FileSend/DiffCopy"
	if !caller.Supports(method) {
		return fmt.Errorf("method %s not supported by the client", method)
	}

	ctx = engine.LocalExportOpts{
		Path:        destPath,
		Merge:       merge,
		RemovePaths: removePaths,
	}.AppendToOutgoingContext(ctx)

	diffCopyClient, err := filesync.NewFileSendClient(caller.Conn()).DiffCopy(ctx)
	if err != nil {
		return fmt.Errorf("failed to create diff copy client: %w", err)
	}
	defer diffCopyClient.CloseSend()

	// Encapsulated like the resolver's "pulling" span: hidden unless it
	// fails, surfacing as a labeled progress row with a climbing byte count
	// (the receiver only requests changed files, so the total is unknown
	// and an up-to-date destination transfers nothing).
	span, ctx := tracing.StartSpan(ctx, "downloading "+destPath, telemetry.Encapsulated(), telemetry.Encapsulate())
	defer func() {
		tracing.FinishWithError(span, rerr)
	}()
	download := bkcache.NewProgressTracker(ctx, "bytes", 0, "bytes")
	defer download.Finish()

	return sendDiffCopyToCaller(diffCopyClient, outputFS, func(current int, _ bool) {
		download.Update(int64(current))
	})
}

func (c *Client) LocalFileExport(
	ctx context.Context,
	srcPath string,
	filePath string,
	destPath string,
	allowParentDirPath bool,
) (rerr error) {
	ctx = slog.WithLogger(ctx, slog.FromContext(ctx).With(
		"export_path", destPath,
		"file_path", filePath,
		"allow_parent_dir_path", allowParentDirPath,
	))
	slog.DebugContext(ctx, "exporting local file")
	defer func() {
		slog.TraceContext(ctx, "finished exporting local file", "err", rerr)
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

	clientCaller, err := c.GetSessionCaller(ctx)
	if err != nil {
		return fmt.Errorf("failed to get requester session: %w", err)
	}
	diffCopyClient, err := filesync.NewFileSendClient(clientCaller.Conn()).DiffCopy(ctx)
	if err != nil {
		return fmt.Errorf("failed to create diff copy client: %w", err)
	}
	defer diffCopyClient.CloseSend()

	// the file size is known up front, so this renders as a 1-D track
	span, ctx := tracing.StartSpan(ctx, "downloading "+destPath, telemetry.Encapsulated(), telemetry.Encapsulate())
	defer func() {
		tracing.FinishWithError(span, rerr)
	}()
	download := bkcache.NewProgressTracker(ctx, "bytes", stat.Size(), "bytes")
	defer download.Finish()

	var sent int64
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
		sent += n
		download.Update(sent)
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
	ctx = slog.WithLogger(ctx, slog.FromContext(ctx).With("export_path", destPath))
	slog.DebugContext(ctx, "exporting bytes")
	defer func() {
		slog.TraceContext(ctx, "finished exporting bytes", "err", rerr)
	}()

	ctx = engine.LocalExportOpts{
		Path:             destPath,
		IsFileStream:     true,
		FileOriginalName: filepath.Base(destPath),
		FileMode:         destMode,
	}.AppendToOutgoingContext(ctx)

	clientCaller, err := c.GetSessionCaller(ctx)
	if err != nil {
		return fmt.Errorf("failed to get requester session: %w", err)
	}
	diffCopyClient, err := filesync.NewFileSendClient(clientCaller.Conn()).DiffCopy(ctx)
	if err != nil {
		return fmt.Errorf("failed to create diff copy client: %w", err)
	}
	defer diffCopyClient.CloseSend()

	// the reader's size is unknown, so this renders as a climbing byte count
	span, ctx := tracing.StartSpan(ctx, "downloading "+destPath, telemetry.Encapsulated(), telemetry.Encapsulate())
	defer func() {
		tracing.FinishWithError(span, rerr)
	}()
	download := bkcache.NewProgressTracker(ctx, "bytes", 0, "bytes")
	defer download.Finish()
	var sent int64

	chunkSize := int64(MaxFileContentsChunkSize)
	keepGoing := true
	for keepGoing {
		buf := new(bytes.Buffer) // TODO: more efficient to use bufio.Writer, reuse buffers, sync.Pool, etc.
		n, err := io.CopyN(buf, r, chunkSize)
		if errors.Is(err, io.EOF) {
			keepGoing = false
			err = nil
		}
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}
		sent += n
		download.Update(sent)
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
