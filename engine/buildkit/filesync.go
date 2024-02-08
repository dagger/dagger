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
	"github.com/dagger/dagger/engine"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session/filesync"
	"github.com/moby/buildkit/snapshot"
	bksolverpb "github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	fsutiltypes "github.com/tonistiigi/fsutil/types"
	"github.com/vito/progrock"
)

func (c *Client) LocalImport(
	ctx context.Context,
	recorder *progrock.Recorder,
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

	localOpts := []llb.LocalOption{
		llb.SessionID(clientMetadata.ClientID),
		llb.SharedKeyHint(strings.Join([]string{clientMetadata.ClientHostname, srcPath}, " ")),
	}

	localName := fmt.Sprintf("upload %s from %s (client id: %s)", srcPath, clientMetadata.ClientHostname, clientMetadata.ClientID)
	if len(excludePatterns) > 0 {
		localName += fmt.Sprintf(" (exclude: %s)", strings.Join(excludePatterns, ", "))
		localOpts = append(localOpts, llb.ExcludePatterns(excludePatterns))
	}
	if len(includePatterns) > 0 {
		localName += fmt.Sprintf(" (include: %s)", strings.Join(includePatterns, ", "))
		localOpts = append(localOpts, llb.IncludePatterns(includePatterns))
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

	RecordVertexes(recorder, copyPB)

	return c.DefToBlob(ctx, copyPB)
}

// Import a directory from the engine container, as opposed to from a client
func (c *Client) EngineContainerLocalImport(
	ctx context.Context,
	recorder *progrock.Recorder,
	platform specs.Platform,
	srcPath string,
	excludePatterns []string,
	includePatterns []string,
) (*bksolverpb.Definition, specs.Descriptor, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, specs.Descriptor{}, fmt.Errorf("failed to get hostname for engine local import: %s", err)
	}
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:       c.ID(),
		ClientHostname: hostname,
	})
	return c.LocalImport(ctx, recorder, platform, srcPath, excludePatterns, includePatterns)
}

func (c *Client) ReadCallerHostFile(ctx context.Context, path string) ([]byte, error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get requester session ID: %s", err)
	}

	ctx = engine.LocalImportOpts{
		OwnerClientID:      clientMetadata.ClientID,
		Path:               path,
		ReadSingleFileOnly: true,
		MaxFileSize:        MaxFileContentsChunkSize,
	}.AppendToOutgoingContext(ctx)

	clientCaller, err := c.SessionManager.Get(ctx, clientMetadata.ClientID, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get requester session: %s", err)
	}
	diffCopyClient, err := filesync.NewFileSyncClient(clientCaller.Conn()).DiffCopy(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create diff copy client: %s", err)
	}
	defer diffCopyClient.CloseSend()
	msg := filesync.BytesMessage{}
	err = diffCopyClient.RecvMsg(&msg)
	if err != nil {
		return nil, fmt.Errorf("failed to receive file bytes message: %s", err)
	}
	return msg.Data, nil
}

func (c *Client) StatCallerHostPath(ctx context.Context, path string, returnAbsPath bool) (*fsutiltypes.Stat, error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get requester session ID: %s", err)
	}

	ctx = engine.LocalImportOpts{
		OwnerClientID:     clientMetadata.ClientID,
		Path:              path,
		StatPathOnly:      true,
		StatReturnAbsPath: returnAbsPath,
	}.AppendToOutgoingContext(ctx)

	clientCaller, err := c.SessionManager.Get(ctx, clientMetadata.ClientID, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get requester session: %s", err)
	}
	diffCopyClient, err := filesync.NewFileSyncClient(clientCaller.Conn()).DiffCopy(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create diff copy client: %s", err)
	}
	defer diffCopyClient.CloseSend()
	msg := fsutiltypes.Stat{}
	err = diffCopyClient.RecvMsg(&msg)
	if err != nil {
		return nil, fmt.Errorf("failed to receive file bytes message: %s", err)
	}
	return &msg, nil
}

func (c *Client) LocalDirExport(
	ctx context.Context,
	def *bksolverpb.Definition,
	destPath string,
) (rerr error) {
	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("export_path", destPath))
	bklog.G(ctx).Debug("exporting local dir")
	defer func() {
		lg := bklog.G(ctx)
		if rerr != nil {
			lg = lg.WithError(rerr)
		}
		lg.Debug("finished exporting local dir")
	}()

	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	destPath = path.Clean(destPath)
	if destPath == ".." || strings.HasPrefix(destPath, "../") {
		return fmt.Errorf("path %q escapes workdir; use an absolute path instead", destPath)
	}

	res, err := c.Solve(ctx, bkgw.SolveRequest{Definition: def})
	if err != nil {
		return fmt.Errorf("failed to solve for local export: %s", err)
	}
	cacheRes, err := ConvertToWorkerCacheResult(ctx, res)
	if err != nil {
		return fmt.Errorf("failed to convert result: %s", err)
	}

	exporter, err := c.Worker.Exporter(bkclient.ExporterLocal, c.SessionManager)
	if err != nil {
		return err
	}

	expInstance, err := exporter.Resolve(ctx, 0, nil)
	if err != nil {
		return fmt.Errorf("failed to resolve exporter: %s", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get requester session ID: %s", err)
	}

	ctx = engine.LocalExportOpts{
		DestClientID: clientMetadata.ClientID,
		Path:         destPath,
	}.AppendToOutgoingContext(ctx)

	_, descRef, err := expInstance.Export(ctx, cacheRes, nil, clientMetadata.ClientID)
	if err != nil {
		return fmt.Errorf("failed to export: %s", err)
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
		lg.Debug("finished exporting local file")
	}()

	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	destPath = path.Clean(destPath)
	if destPath == ".." || strings.HasPrefix(destPath, "../") {
		return fmt.Errorf("path %q escapes workdir; use an absolute path instead", destPath)
	}

	res, err := c.Solve(ctx, bkgw.SolveRequest{Definition: def, Evaluate: true})
	if err != nil {
		return fmt.Errorf("failed to solve for local export: %s", err)
	}
	ref, err := res.SingleRef()
	if err != nil {
		return fmt.Errorf("failed to get single ref: %s", err)
	}

	mountable, err := ref.getMountable(ctx)
	if err != nil {
		return fmt.Errorf("failed to get mountable: %s", err)
	}
	mounter := snapshot.LocalMounter(mountable)
	mountPath, err := mounter.Mount()
	if err != nil {
		return fmt.Errorf("failed to mount: %s", err)
	}
	defer mounter.Unmount()
	mntFilePath, err := fs.RootPath(mountPath, filePath)
	if err != nil {
		return fmt.Errorf("failed to get root path: %s", err)
	}
	file, err := os.Open(mntFilePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %s", err)
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %s", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get requester session ID: %s", err)
	}

	ctx = engine.LocalExportOpts{
		DestClientID:       clientMetadata.ClientID,
		Path:               destPath,
		IsFileStream:       true,
		FileOriginalName:   filepath.Base(filePath),
		AllowParentDirPath: allowParentDirPath,
		FileMode:           stat.Mode().Perm(),
	}.AppendToOutgoingContext(ctx)

	clientCaller, err := c.SessionManager.Get(ctx, clientMetadata.ClientID, false)
	if err != nil {
		return fmt.Errorf("failed to get requester session: %s", err)
	}
	diffCopyClient, err := filesync.NewFileSendClient(clientCaller.Conn()).DiffCopy(ctx)
	if err != nil {
		return fmt.Errorf("failed to create diff copy client: %s", err)
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
			return fmt.Errorf("failed to read file: %s", err)
		}
		fileSizeLeft -= n
		err = diffCopyClient.SendMsg(&filesync.BytesMessage{Data: buf.Bytes()})
		if errors.Is(err, io.EOF) {
			err := diffCopyClient.RecvMsg(struct{}{})
			if err != nil {
				return fmt.Errorf("diff copy client error: %s", err)
			}
		} else if err != nil {
			return fmt.Errorf("failed to send file chunk: %s", err)
		}
	}
	if err := diffCopyClient.CloseSend(); err != nil {
		return fmt.Errorf("failed to close send: %s", err)
	}
	// wait for receiver to finish
	var msg filesync.BytesMessage
	if err := diffCopyClient.RecvMsg(&msg); err != io.EOF {
		return fmt.Errorf("unexpected closing recv msg: %s", err)
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
		lg.Debug("finished exporting bytes")
	}()

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get requester session ID: %s", err)
	}

	ctx = engine.LocalExportOpts{
		DestClientID:     clientMetadata.ClientID,
		Path:             destPath,
		IsFileStream:     true,
		FileOriginalName: filepath.Base(destPath),
		FileMode:         destMode,
	}.AppendToOutgoingContext(ctx)

	clientCaller, err := c.SessionManager.Get(ctx, clientMetadata.ClientID, false)
	if err != nil {
		return fmt.Errorf("failed to get requester session: %s", err)
	}
	diffCopyClient, err := filesync.NewFileSendClient(clientCaller.Conn()).DiffCopy(ctx)
	if err != nil {
		return fmt.Errorf("failed to create diff copy client: %s", err)
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
			return fmt.Errorf("failed to read file: %s", err)
		}
		err = diffCopyClient.SendMsg(&filesync.BytesMessage{Data: buf.Bytes()})
		if errors.Is(err, io.EOF) {
			err := diffCopyClient.RecvMsg(struct{}{})
			if err != nil {
				return fmt.Errorf("diff copy client error: %s", err)
			}
		} else if err != nil {
			return fmt.Errorf("failed to send file chunk: %s", err)
		}
	}
	if err := diffCopyClient.CloseSend(); err != nil {
		return fmt.Errorf("failed to close send: %s", err)
	}
	// wait for receiver to finish
	var msg filesync.BytesMessage
	if err := diffCopyClient.RecvMsg(&msg); err != io.EOF {
		return fmt.Errorf("unexpected closing recv msg: %s", err)
	}
	return nil
}
