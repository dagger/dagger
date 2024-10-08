package buildkit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/containerd/continuity/fs"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/session/filesync"
	"github.com/moby/buildkit/snapshot"
	bksolverpb "github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/compression"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	fsutiltypes "github.com/tonistiigi/fsutil/types"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
)

func (c *Client) LocalImport(
	ctx context.Context,
	platform specs.Platform,
	srcPath string,
	excludePatterns []string,
	includePatterns []string,
) (*bksolverpb.Definition, specs.Descriptor, error) {
	var desc specs.Descriptor

	srcPath = path.Clean(srcPath)
	if srcPath == ".." || strings.HasPrefix(srcPath, "../") {
		return nil, desc, fmt.Errorf("path %q escapes workdir; use an absolute path instead", srcPath)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, desc, err
	}

	stableID := clientMetadata.ClientStableID
	if stableID == "" {
		slog.WarnContext(ctx, "client stable ID not set, using random value")
		stableID = identity.NewID()
	}

	localOpts := []llb.LocalOption{
		llb.SessionID(clientMetadata.ClientID),
		llb.SharedKeyHint(strings.Join([]string{stableID, srcPath}, " ")),
	}

	localName := fmt.Sprintf("upload %s from %s (client id: %s, session id: %s)", srcPath, stableID, clientMetadata.ClientID, clientMetadata.SessionID)
	if len(includePatterns) > 0 {
		localName += fmt.Sprintf(" (include: %s)", strings.Join(includePatterns, ", "))
		localOpts = append(localOpts, llb.IncludePatterns(includePatterns))
	}
	if len(excludePatterns) > 0 {
		localName += fmt.Sprintf(" (exclude: %s)", strings.Join(excludePatterns, ", "))
		localOpts = append(localOpts, llb.ExcludePatterns(excludePatterns))
	}
	localOpts = append(localOpts, llb.WithCustomName(localName))
	localLLB := llb.Local(srcPath, localOpts...)

	// We still need to do a copy here for now because buildkit's cache calls
	// Finalize on refs when getting their blobs which makes the cache ref for the
	// local ref unable to be reused.
	//
	// TODO: we should ensure that this doesn't create a new cache entry, without
	// this the entire local directory is uploaded to the cache. See also
	// blobSource.CacheKey for more context
	copyLLB := llb.Scratch().File(
		llb.Copy(localLLB, "/", "/"),
		llb.WithCustomNamef("%scopy %s", InternalPrefix, localName),
	)

	copyDef, err := copyLLB.Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, desc, err
	}
	copyPB := copyDef.ToPB()

	return c.DefToBlob(ctx, copyPB, compression.Zstd)
}

// Import a directory from the engine container, as opposed to from a client
func (c *Client) EngineContainerLocalImport(
	ctx context.Context,
	platform specs.Platform,
	srcPath string,
	excludePatterns []string,
	includePatterns []string,
) (*bksolverpb.Definition, specs.Descriptor, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, specs.Descriptor{}, fmt.Errorf("failed to get hostname for engine local import: %w", err)
	}
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:       c.ID(),
		ClientHostname: hostname,
	})
	return c.LocalImport(ctx, platform, srcPath, excludePatterns, includePatterns)
}

func (c *Client) diffcopy(ctx context.Context, opts engine.LocalImportOpts, msg any) error {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return err
	}
	defer cancel(errors.New("diff copy done"))

	ctx = opts.AppendToOutgoingContext(ctx)

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

func (c *Client) LocalDirExport(
	ctx context.Context,
	def *bksolverpb.Definition,
	destPath string,
	merge bool,
) (rerr error) {
	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("export_path", destPath))
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

	destPath = path.Clean(destPath)
	if destPath == ".." || strings.HasPrefix(destPath, "../") {
		return fmt.Errorf("path %q escapes workdir; use an absolute path instead", destPath)
	}

	res, err := c.Solve(ctx, bkgw.SolveRequest{Definition: def})
	if err != nil {
		return fmt.Errorf("failed to solve for local export: %w", err)
	}
	cacheRes, err := ConvertToWorkerCacheResult(ctx, res)
	if err != nil {
		return fmt.Errorf("failed to convert result: %w", err)
	}

	exporter, err := c.Worker.Exporter(bkclient.ExporterLocal, c.SessionManager)
	if err != nil {
		return err
	}

	expInstance, err := exporter.Resolve(ctx, 0, nil)
	if err != nil {
		return fmt.Errorf("failed to resolve exporter: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get requester session ID: %w", err)
	}

	ctx = engine.LocalExportOpts{
		Path:  destPath,
		Merge: merge,
	}.AppendToOutgoingContext(ctx)

	_, descRef, err := expInstance.Export(ctx, cacheRes, nil, clientMetadata.ClientID)
	if err != nil {
		return fmt.Errorf("failed to export: %w", err)
	}
	if descRef != nil {
		descRef.Release()
	}
	return nil
}

func (c *Client) LocalFileExport(
	ctx context.Context,
	def *bksolverpb.Definition,
	destPath string,
	filePath string,
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
	if destPath == ".." || strings.HasPrefix(destPath, "../") {
		return fmt.Errorf("path %q escapes workdir; use an absolute path instead", destPath)
	}

	res, err := c.Solve(ctx, bkgw.SolveRequest{Definition: def, Evaluate: true})
	if err != nil {
		return fmt.Errorf("failed to solve for local export: %w", err)
	}
	ref, err := res.SingleRef()
	if err != nil {
		return fmt.Errorf("failed to get single ref: %w", err)
	}

	mountable, err := ref.getMountable(ctx)
	if err != nil {
		return fmt.Errorf("failed to get mountable: %w", err)
	}
	mounter := snapshot.LocalMounter(mountable)
	mountPath, err := mounter.Mount()
	if err != nil {
		return fmt.Errorf("failed to mount: %w", err)
	}
	defer mounter.Unmount()
	mntFilePath, err := fs.RootPath(mountPath, filePath)
	if err != nil {
		return fmt.Errorf("failed to get root path: %w", err)
	}
	file, err := os.Open(mntFilePath)
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
