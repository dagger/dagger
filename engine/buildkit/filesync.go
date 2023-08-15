package buildkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/engine"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/filesync"
	"github.com/moby/buildkit/snapshot"
	bksolverpb "github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	filesynctypes "github.com/tonistiigi/fsutil/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func (c *Client) LocalImportLLB(ctx context.Context, srcPath string, opts ...llb.LocalOption) (llb.State, error) {
	srcPath = path.Clean(srcPath)
	if srcPath == ".." || strings.HasPrefix(srcPath, "../") {
		return llb.State{}, fmt.Errorf("path %q escapes workdir; use an absolute path instead", srcPath)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return llb.State{}, err
	}

	localImportOpts := engine.LocalImportOpts{
		// For now, the requester is always the owner of the local dir
		// when the dir is initially created in LLB (i.e. you can't request a
		// a new local dir from another session, you can only be passed one
		// from another session already created).
		OwnerClientID: clientMetadata.ClientID,
		Path:          srcPath,
	}

	// set any buildkit llb options too
	llbLocalOpts := &llb.LocalInfo{}
	for _, opt := range opts {
		opt.SetLocalOption(llbLocalOpts)
	}
	// llb marshals lists as json for some reason
	if len(llbLocalOpts.IncludePatterns) > 0 {
		if err := json.Unmarshal([]byte(llbLocalOpts.IncludePatterns), &localImportOpts.IncludePatterns); err != nil {
			return llb.State{}, err
		}
	}
	if len(llbLocalOpts.ExcludePatterns) > 0 {
		if err := json.Unmarshal([]byte(llbLocalOpts.ExcludePatterns), &localImportOpts.ExcludePatterns); err != nil {
			return llb.State{}, err
		}
	}
	if len(llbLocalOpts.FollowPaths) > 0 {
		if err := json.Unmarshal([]byte(llbLocalOpts.FollowPaths), &localImportOpts.FollowPaths); err != nil {
			return llb.State{}, err
		}
	}

	// NOTE: this relies on the fact that the local source is evaluated synchronously in the caller, otherwise
	// the caller client ID may not be correct.
	name, err := EncodeIDHack(localImportOpts)
	if err != nil {
		return llb.State{}, err
	}

	opts = append(opts,
		// synchronize concurrent filesyncs for the same srcPath
		llb.SharedKeyHint(name),
		llb.SessionID(c.ID()),
	)
	return llb.Local(name, opts...), nil
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

func (c *Client) LocalDirExport(
	ctx context.Context,
	def *bksolverpb.Definition,
	destPath string,
) error {
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

	expInstance, err := exporter.Resolve(ctx, nil)
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

	_, descRef, err := expInstance.Export(ctx, cacheRes, c.ID())
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
) error {
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

	fileStat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %s", err)
	}
	fileSizeLeft := fileStat.Size()
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

type localImportOpts struct {
	*engine.LocalImportOpts
	session bksession.Caller
}

func (c *Client) getLocalImportOpts(stream grpc.ServerStream) (context.Context, *localImportOpts, error) {
	opts, err := engine.LocalImportOptsFromContext(stream.Context())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get local import opts: %w", err)
	}

	if err := DecodeIDHack("local", opts.Path, opts); err != nil {
		return nil, nil, fmt.Errorf("invalid import local dir name: %q", opts.Path)
	}
	sess, err := c.SessionManager.Get(stream.Context(), opts.OwnerClientID, false)
	if err != nil {
		return nil, nil, err
	}
	md := opts.ToGRPCMD()
	ctx := metadata.NewIncomingContext(stream.Context(), md) // TODO: needed too?
	ctx = metadata.NewOutgoingContext(ctx, md)
	return ctx, &localImportOpts{
		LocalImportOpts: opts,
		session:         sess,
	}, nil
}

type localExportOpts struct {
	*engine.LocalExportOpts
	session bksession.Caller
}

func (c *Client) getLocalExportOpts(stream grpc.ServerStream) (context.Context, *localExportOpts, error) {
	opts, err := engine.LocalExportOptsFromContext(stream.Context())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get local export opts: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(stream.Context())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get client metadata: %w", err)
	}

	// for now, require that the requester is the owner of the session, i.e. you
	// can only export to yourself, not to others
	if clientMetadata.ClientID != opts.DestClientID {
		return nil, nil, errors.New("local dir export requester is not the owner of the dest session")
	}

	if opts.Path == "" {
		return nil, nil, fmt.Errorf("missing local dir export path")
	}

	sess, err := c.SessionManager.Get(stream.Context(), opts.DestClientID, false)
	if err != nil {
		return nil, nil, err
	}
	md := opts.ToGRPCMD()
	ctx := metadata.NewIncomingContext(stream.Context(), md) // TODO: needed too?
	ctx = metadata.NewOutgoingContext(ctx, md)
	return ctx, &localExportOpts{
		LocalExportOpts: opts,
		session:         sess,
	}, nil
}

// for local dir imports
type fileSyncServerProxy struct {
	c *Client
}

func (p *fileSyncServerProxy) Register(srv *grpc.Server) {
	filesync.RegisterFileSyncServer(srv, p)
}

func (p *fileSyncServerProxy) DiffCopy(stream filesync.FileSync_DiffCopyServer) error {
	ctx, opts, err := p.c.getLocalImportOpts(stream)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	diffCopyClient, err := filesync.NewFileSyncClient(opts.session.Conn()).DiffCopy(ctx)
	if err != nil {
		return err
	}
	return proxyStream[filesynctypes.Packet](ctx, diffCopyClient, stream)
}

func (p *fileSyncServerProxy) TarStream(stream filesync.FileSync_TarStreamServer) error {
	ctx, opts, err := p.c.getLocalImportOpts(stream)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tarStreamClient, err := filesync.NewFileSyncClient(opts.session.Conn()).TarStream(ctx)
	if err != nil {
		return err
	}
	return proxyStream[filesynctypes.Packet](ctx, tarStreamClient, stream)
}

// TODO: workaround needed until upstream fix: https://github.com/moby/buildkit/pull/4049
func (c *Client) newFileSendServerProxySession(ctx context.Context, destPath string) (*bksession.Session, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get requester client metadata: %s", err)
	}
	sess, err := bksession.NewSession(ctx, identity.NewID(), "")
	if err != nil {
		return nil, err
	}
	proxy := &fileSendServerProxy{c: c, destClientID: clientMetadata.ClientID, destPath: destPath}
	sess.Allow(proxy)

	clientConn, serverConn := net.Pipe()
	dialer := func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) { // nolint: unparam
		go func() {
			defer serverConn.Close()
			err := c.SessionManager.HandleConn(ctx, serverConn, meta)
			if err != nil {
				lg := bklog.G(ctx).WithError(err)
				if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
					lg.Debug("session conn ended")
				} else {
					lg.Error("failed to handle session conn")
				}
			}
		}()
		return clientConn, nil
	}
	go func() {
		defer clientConn.Close()
		defer sess.Close()
		err := sess.Run(ctx, dialer)
		if err != nil {
			lg := bklog.G(ctx).WithError(err)
			if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				lg.Debug("client session in dagger frontend ended")
			} else {
				lg.Error("failed to run dagger frontend session")
			}
		}
	}()

	return sess, nil
}

// for local dir exports
type fileSendServerProxy struct {
	c *Client
	// TODO: workaround needed until upstream fix: https://github.com/moby/buildkit/pull/4049
	destClientID string
	destPath     string
}

func (p *fileSendServerProxy) Register(srv *grpc.Server) {
	filesync.RegisterFileSendServer(srv, p)
}

func (p *fileSendServerProxy) DiffCopy(stream filesync.FileSend_DiffCopyServer) (rerr error) {
	ctx := stream.Context()
	var diffCopyClient filesync.FileSend_DiffCopyClient
	var err error
	var useBytesMessageType bool
	if p.destClientID == "" {
		var opts *localExportOpts
		ctx, opts, err = p.c.getLocalExportOpts(stream)
		if err != nil {
			return err
		}
		diffCopyClient, err = filesync.NewFileSendClient(opts.session.Conn()).DiffCopy(ctx)
		if err != nil {
			return err
		}
	} else {
		// TODO: workaround needed until upstream fix: https://github.com/moby/buildkit/pull/4049
		useBytesMessageType = true
		ctx = engine.LocalExportOpts{
			DestClientID: p.destClientID,
			Path:         p.destPath,
			IsFileStream: true,
		}.AppendToOutgoingContext(ctx)

		destCaller, err := p.c.SessionManager.Get(ctx, p.destClientID, false)
		if err != nil {
			return fmt.Errorf("failed to get requester client session: %s", err)
		}
		diffCopyClient, err = filesync.NewFileSendClient(destCaller.Conn()).DiffCopy(ctx)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if useBytesMessageType {
		return proxyStream[filesync.BytesMessage](ctx, diffCopyClient, stream)
	}
	return proxyStream[filesynctypes.Packet](ctx, diffCopyClient, stream)
}
