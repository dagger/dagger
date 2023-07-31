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
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/platforms"
	"github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/engine"
	bkcache "github.com/moby/buildkit/cache"
	bkcacheconfig "github.com/moby/buildkit/cache/config"
	"github.com/moby/buildkit/cache/remotecache"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	bkfrontend "github.com/moby/buildkit/frontend"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bkcontainer "github.com/moby/buildkit/frontend/gateway/container"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/filesync"
	bksecrets "github.com/moby/buildkit/session/secrets"
	"github.com/moby/buildkit/snapshot"
	bksolver "github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/llbsolver"
	bksolverpb "github.com/moby/buildkit/solver/pb"
	solverresult "github.com/moby/buildkit/solver/result"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/entitlements"
	bkworker "github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/metadata"
)

const (
	// from buildkit, cannot change
	entitlementsJobKey = "llb.entitlements"
)

type Opts struct {
	Worker                bkworker.Worker
	SessionManager        *bksession.Manager
	LLBSolver             *llbsolver.Solver
	GenericSolver         *bksolver.Solver
	SecretStore           bksecrets.SecretStore
	AuthProvider          *auth.RegistryAuthProvider
	PrivilegedExecEnabled bool
	UpstreamCacheImports  []bkgw.CacheOptionsEntry
	// MainClientCaller is the caller who initialized the server associated with this
	// client. It is special in that when it shuts down, the client will be closed and
	// that registry auth and sockets are currently only ever sourced from this caller,
	// not any nested clients (may change in future).
	MainClientCaller bksession.Caller
}

type ResolveCacheExporterFunc func(ctx context.Context, g bksession.Group) (remotecache.Exporter, error)

// Client is dagger's internal interface to buildkit APIs
type Client struct {
	Opts
	session   *bksession.Session
	job       *bksolver.Job
	llbBridge bkfrontend.FrontendLLBBridge

	clientMu              sync.RWMutex
	clientIDToSecretToken map[string]string

	refs         map[*ref]struct{}
	refsMu       sync.Mutex
	containers   map[bkgw.Container]struct{}
	containersMu sync.Mutex

	closeCtx context.Context
	cancel   context.CancelFunc
	closeMu  sync.RWMutex
}

type Result = solverresult.Result[*ref]

func NewClient(ctx context.Context, opts Opts) (*Client, error) {
	closeCtx, cancel := context.WithCancel(context.Background())
	client := &Client{
		Opts:                  opts,
		clientIDToSecretToken: make(map[string]string),
		refs:                  make(map[*ref]struct{}),
		containers:            make(map[bkgw.Container]struct{}),
		closeCtx:              closeCtx,
		cancel:                cancel,
	}

	session, err := client.newSession(ctx)
	if err != nil {
		return nil, err
	}
	client.session = session

	job, err := client.GenericSolver.NewJob(client.ID())
	if err != nil {
		return nil, err
	}
	client.job = job
	client.job.SessionID = client.ID()

	entitlementSet := entitlements.Set{}
	if opts.PrivilegedExecEnabled {
		entitlementSet[entitlements.EntitlementSecurityInsecure] = struct{}{}
	}
	client.job.SetValue(entitlementsJobKey, entitlementSet)

	client.llbBridge = client.LLBSolver.Bridge(client.job)
	client.llbBridge = recordingGateway{client.llbBridge}

	return client, nil
}

func (c *Client) ID() string {
	return c.session.ID()
}

func (c *Client) Close() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	select {
	case <-c.closeCtx.Done():
		// already closed
		return nil
	default:
	}
	c.cancel()

	c.job.Discard()
	c.job.CloseProgress()

	c.refsMu.Lock()
	for rf := range c.refs {
		if rf != nil {
			rf.resultProxy.Release(context.Background())
		}
	}
	c.refs = nil
	c.refsMu.Unlock()

	c.containersMu.Lock()
	var containerReleaseGroup errgroup.Group
	for ctr := range c.containers {
		if ctr := ctr; ctr != nil {
			containerReleaseGroup.Go(func() error {
				// in theory this shouldn't block very long and just kill the container,
				// but add a safeguard just in case
				releaseCtx, cancelRelease := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancelRelease()
				return ctr.Release(releaseCtx)
			})
		}
	}
	err := containerReleaseGroup.Wait()
	if err != nil {
		bklog.G(context.Background()).WithError(err).Error("failed to release containers")
	}
	c.containers = nil
	c.containersMu.Unlock()

	return nil
}

func (c *Client) withClientCloseCancel(ctx context.Context) (context.Context, context.CancelFunc, error) {
	c.closeMu.RLock()
	defer c.closeMu.RUnlock()
	select {
	case <-c.closeCtx.Done():
		return nil, nil, errors.New("client closed")
	default:
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-c.closeCtx.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel, nil
}

func (c *Client) Solve(ctx context.Context, req bkgw.SolveRequest) (_ *Result, rerr error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	ctx = withOutgoingContext(ctx)

	// include upstream cache imports, if any
	req.CacheImports = c.UpstreamCacheImports

	llbRes, err := c.llbBridge.Solve(ctx, req, c.ID())
	if err != nil {
		return nil, wrapError(ctx, err, c.ID())
	}
	res, err := solverresult.ConvertResult(llbRes, func(rp bksolver.ResultProxy) (*ref, error) {
		return newRef(rp, c), nil
	})
	if err != nil {
		llbRes.EachRef(func(rp bksolver.ResultProxy) error {
			return rp.Release(context.Background())
		})
		return nil, err
	}

	c.refsMu.Lock()
	defer c.refsMu.Unlock()
	if res.Ref != nil {
		c.refs[res.Ref] = struct{}{}
	}
	for _, rf := range res.Refs {
		c.refs[rf] = struct{}{}
	}
	return res, nil
}

func (c *Client) ResolveImageConfig(ctx context.Context, ref string, opt llb.ResolveImageConfigOpt) (digest.Digest, []byte, error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return "", nil, err
	}
	defer cancel()
	ctx = withOutgoingContext(ctx)
	_, digest, configBytes, err := c.llbBridge.ResolveImageConfig(ctx, ref, opt)
	return digest, configBytes, err
}

func (c *Client) NewContainer(ctx context.Context, req bkgw.NewContainerRequest) (bkgw.Container, error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	ctx = withOutgoingContext(ctx)
	ctrReq := bkcontainer.NewContainerRequest{
		ContainerID: identity.NewID(),
		NetMode:     req.NetMode,
		Hostname:    req.Hostname,
		Mounts:      make([]bkcontainer.Mount, len(req.Mounts)),
	}

	extraHosts, err := bkcontainer.ParseExtraHosts(req.ExtraHosts)
	if err != nil {
		return nil, err
	}
	ctrReq.ExtraHosts = extraHosts

	// get the input mounts in parallel in case they need to be evaluated, which can be expensive
	eg, egctx := errgroup.WithContext(ctx)
	for i, m := range req.Mounts {
		i, m := i, m
		eg.Go(func() error {
			ref, ok := m.Ref.(*ref)
			if !ok {
				return fmt.Errorf("unexpected ref type: %T", m.Ref)
			}
			var workerRef *bkworker.WorkerRef
			if ref != nil {
				res, err := ref.resultProxy.Result(egctx)
				if err != nil {
					return err
				}
				var ok bool
				workerRef, ok = res.Sys().(*bkworker.WorkerRef)
				if !ok {
					return fmt.Errorf("invalid res: %T", res.Sys())
				}
			}
			ctrReq.Mounts[i] = bkcontainer.Mount{
				WorkerRef: workerRef,
				Mount: &bksolverpb.Mount{
					Dest:      m.Dest,
					Selector:  m.Selector,
					Readonly:  m.Readonly,
					MountType: m.MountType,
					CacheOpt:  m.CacheOpt,
					SecretOpt: m.SecretOpt,
					SSHOpt:    m.SSHOpt,
				},
			}
			return nil
		})
	}
	err = eg.Wait()
	if err != nil {
		return nil, err
	}

	// using context.Background so it continues running until exit or when c.Close() is called
	ctr, err := bkcontainer.NewContainer(
		context.Background(),
		c.Worker,
		c.SessionManager,
		bksession.NewGroup(c.ID()),
		ctrReq,
	)
	if err != nil {
		return nil, err
	}

	c.containersMu.Lock()
	defer c.containersMu.Unlock()
	c.containers[ctr] = struct{}{}
	return ctr, nil
}

// CombinedResult returns a buildkit result with all the refs solved by this client so far.
// This is useful for constructing a result for upstream remote caching.
func (c *Client) CombinedResult(ctx context.Context) (*Result, error) {
	c.refsMu.Lock()
	mergeInputs := make([]llb.State, 0, len(c.refs))
	for r := range c.refs {
		state, err := r.ToState()
		if err != nil {
			c.refsMu.Unlock()
			return nil, err
		}
		mergeInputs = append(mergeInputs, state)
	}
	c.refsMu.Unlock()
	llbdef, err := llb.Merge(mergeInputs, llb.WithCustomName("combined session result")).Marshal(ctx)
	if err != nil {
		return nil, err
	}
	return c.Solve(ctx, bkgw.SolveRequest{
		Definition: llbdef.ToPB(),
	})
}

func (c *Client) UpstreamCacheExport(ctx context.Context, cacheExportFuncs []ResolveCacheExporterFunc) error {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	if len(cacheExportFuncs) == 0 {
		return nil
	}
	bklog.G(ctx).Debugf("exporting %d caches", len(cacheExportFuncs))

	combinedResult, err := c.CombinedResult(ctx)
	if err != nil {
		return err
	}
	// TODO: dedupe with similar conversions
	cacheRes, err := solverresult.ConvertResult(combinedResult, func(rf *ref) (bkcache.ImmutableRef, error) {
		res, err := rf.Result(ctx)
		if err != nil {
			return nil, err
		}
		workerRef, ok := res.Sys().(*bkworker.WorkerRef)
		if !ok {
			return nil, fmt.Errorf("invalid ref: %T", res.Sys())
		}
		return workerRef.ImmutableRef, nil
	})
	if err != nil {
		return fmt.Errorf("failed to convert result: %s", err)
	}
	bklog.G(ctx).Debugf("converting to solverRes")
	solverRes, err := solverresult.ConvertResult(combinedResult, func(rf *ref) (bksolver.CachedResult, error) {
		return rf.resultProxy.Result(ctx)
	})
	if err != nil {
		return fmt.Errorf("failed to convert result: %s", err)
	}

	sessionGroup := bksession.NewGroup(c.ID())
	eg, ctx := errgroup.WithContext(ctx)
	// TODO: send progrock statuses for cache export progress
	for _, exporterFunc := range cacheExportFuncs {
		exporterFunc := exporterFunc
		eg.Go(func() error {
			bklog.G(ctx).Debugf("getting exporter")
			exporter, err := exporterFunc(ctx, sessionGroup)
			if err != nil {
				return err
			}
			bklog.G(ctx).Debugf("exporting cache with %T", exporter)
			compressionCfg := exporter.Config().Compression
			err = solverresult.EachRef(solverRes, cacheRes, func(res bksolver.CachedResult, ref bkcache.ImmutableRef) error {
				bklog.G(ctx).Debugf("exporting cache for %s", ref.ID())
				ctx := withDescHandlerCacheOpts(ctx, ref)
				bklog.G(ctx).Debugf("calling exporter")
				_, err = res.CacheKeys()[0].Exporter.ExportTo(ctx, exporter, bksolver.CacheExportOpt{
					ResolveRemotes: func(ctx context.Context, res bksolver.Result) ([]*bksolver.Remote, error) {
						ref, ok := res.Sys().(*bkworker.WorkerRef)
						if !ok {
							return nil, fmt.Errorf("invalid result: %T", res.Sys())
						}
						bklog.G(ctx).Debugf("getting remotes for %s", ref.ID())
						defer bklog.G(ctx).Debugf("got remotes for %s", ref.ID())
						return ref.GetRemotes(ctx, true, bkcacheconfig.RefConfig{Compression: compressionCfg}, false, sessionGroup)
					},
					Mode:           bksolver.CacheExportModeMax,
					Session:        sessionGroup,
					CompressionOpt: &compressionCfg,
				})
				return err
			})
			if err != nil {
				return err
			}
			bklog.G(ctx).Debugf("finalizing exporter")
			defer bklog.G(ctx).Debugf("finalized exporter")
			_, err = exporter.Finalize(ctx)
			return err
		})
	}
	bklog.G(ctx).Debugf("waiting for cache export")
	defer bklog.G(ctx).Debugf("waited for cache export")
	return eg.Wait()
}

func withDescHandlerCacheOpts(ctx context.Context, ref bkcache.ImmutableRef) context.Context {
	return bksolver.WithCacheOptGetter(ctx, func(_ bool, keys ...interface{}) map[interface{}]interface{} {
		vals := make(map[interface{}]interface{})
		for _, k := range keys {
			if key, ok := k.(bkcache.DescHandlerKey); ok {
				if handler := ref.DescHandler(digest.Digest(key)); handler != nil {
					vals[k] = handler
				}
			}
		}
		return vals
	})
}

func (c *Client) WriteStatusesTo(ctx context.Context, ch chan *bkclient.SolveStatus) error {
	return c.job.Status(ctx, ch)
}

func (c *Client) RegisterClient(clientID, clientHostname, secretToken string) error {
	c.clientMu.Lock()
	defer c.clientMu.Unlock()
	existingToken, ok := c.clientIDToSecretToken[clientID]
	if ok {
		if existingToken != secretToken {
			return fmt.Errorf("client ID %q already registered with different secret token", clientID)
		}
		return nil
	}
	c.clientIDToSecretToken[clientID] = secretToken
	// NOTE: we purposely don't delete the secret token, it should never be reused and will be released
	// from memory once the dagger server instance corresponding to this buildkit client shuts down.
	// Deleting it would make it easier to create race conditions around using the client's session
	// before it is fully closed.
	return nil
}

func (c *Client) VerifyClient(clientID, secretToken string) error {
	c.clientMu.RLock()
	defer c.clientMu.RUnlock()
	existingToken, ok := c.clientIDToSecretToken[clientID]
	if !ok {
		return fmt.Errorf("client ID %q not registered", clientID)
	}
	if existingToken != secretToken {
		return fmt.Errorf("client ID %q registered with different secret token", clientID)
	}
	return nil
}

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

	cacheRes, err := solverresult.ConvertResult(res, func(rf *ref) (bkcache.ImmutableRef, error) {
		cachedRes, err := rf.Result(ctx)
		if err != nil {
			return nil, err
		}
		workerRef, ok := cachedRes.Sys().(*bkworker.WorkerRef)
		if !ok {
			return nil, fmt.Errorf("invalid ref: %T", cachedRes.Sys())
		}
		return workerRef.ImmutableRef, nil
	})
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

type PublishInput struct {
	Definition *bksolverpb.Definition
	Config     specs.ImageConfig
}

func (c *Client) PublishContainerImage(
	ctx context.Context,
	inputByPlatform map[string]PublishInput,
	opts map[string]string, // TODO: make this an actual type, this leaks too much untyped buildkit api
) (map[string]string, error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()

	combinedResult := &solverresult.Result[bkcache.ImmutableRef]{}
	expPlatforms := &exptypes.Platforms{
		Platforms: make([]exptypes.Platform, len(inputByPlatform)),
	}
	// TODO: probably faster to do this in parallel for each platform
	for platformString, input := range inputByPlatform {
		// TODO: add util for turning into cacheRes, dedupe w/ above
		res, err := c.Solve(ctx, bkgw.SolveRequest{Definition: input.Definition})
		if err != nil {
			return nil, fmt.Errorf("failed to solve for container publish: %s", err)
		}
		cacheRes, err := solverresult.ConvertResult(res, func(rf *ref) (bkcache.ImmutableRef, error) {
			res, err := rf.Result(ctx)
			if err != nil {
				return nil, err
			}
			workerRef, ok := res.Sys().(*bkworker.WorkerRef)
			if !ok {
				return nil, fmt.Errorf("invalid ref: %T", res.Sys())
			}
			return workerRef.ImmutableRef, nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to convert result: %s", err)
		}
		ref, err := cacheRes.SingleRef()
		if err != nil {
			return nil, err
		}

		platform, err := platforms.Parse(platformString)
		if err != nil {
			return nil, err
		}
		cfgBytes, err := json.Marshal(specs.Image{
			Platform: specs.Platform{
				Architecture: platform.Architecture,
				OS:           platform.OS,
				OSVersion:    platform.OSVersion,
				OSFeatures:   platform.OSFeatures,
			},
			Config: input.Config,
		})
		if err != nil {
			return nil, err
		}
		if len(inputByPlatform) == 1 {
			combinedResult.AddMeta(exptypes.ExporterImageConfigKey, cfgBytes)
			combinedResult.SetRef(ref)
		} else {
			combinedResult.AddMeta(fmt.Sprintf("%s/%s", exptypes.ExporterImageConfigKey, platformString), cfgBytes)
			expPlatforms.Platforms[len(combinedResult.Refs)] = exptypes.Platform{
				ID:       platformString,
				Platform: platform,
			}
			combinedResult.AddRef(platformString, ref)
		}
	}
	if len(combinedResult.Refs) > 1 {
		platformBytes, err := json.Marshal(expPlatforms)
		if err != nil {
			return nil, err
		}
		combinedResult.AddMeta(exptypes.ExporterPlatformsKey, platformBytes)
	}

	exporter, err := c.Worker.Exporter(bkclient.ExporterImage, c.SessionManager)
	if err != nil {
		return nil, err
	}

	expInstance, err := exporter.Resolve(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve exporter: %s", err)
	}

	resp, descRef, err := expInstance.Export(ctx, combinedResult, c.ID())
	if err != nil {
		return nil, fmt.Errorf("failed to export: %s", err)
	}
	if descRef != nil {
		descRef.Release()
	}
	return resp, nil
}

// TODO: dedupe w/ above
func (c *Client) ExportContainerImage(
	ctx context.Context,
	inputByPlatform map[string]PublishInput, //TODO: publish input as a name makes no sense anymore
	destPath string,
	opts map[string]string, // TODO: make this an actual type, this leaks too much untyped buildkit api
) (map[string]string, error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()

	destPath = path.Clean(destPath)
	if destPath == ".." || strings.HasPrefix(destPath, "../") {
		return nil, fmt.Errorf("path %q escapes workdir; use an absolute path instead", destPath)
	}

	combinedResult := &solverresult.Result[bkcache.ImmutableRef]{}
	expPlatforms := &exptypes.Platforms{
		Platforms: make([]exptypes.Platform, 0, len(inputByPlatform)),
	}
	// TODO: probably faster to do this in parallel for each platform
	for platformString, input := range inputByPlatform {
		// TODO: add util for turning into cacheRes, dedupe w/ above
		res, err := c.Solve(ctx, bkgw.SolveRequest{Definition: input.Definition})
		if err != nil {
			return nil, fmt.Errorf("failed to solve for container publish: %s", err)
		}
		cacheRes, err := solverresult.ConvertResult(res, func(rf *ref) (bkcache.ImmutableRef, error) {
			res, err := rf.Result(ctx)
			if err != nil {
				return nil, err
			}
			workerRef, ok := res.Sys().(*bkworker.WorkerRef)
			if !ok {
				return nil, fmt.Errorf("invalid ref: %T", res.Sys())
			}
			return workerRef.ImmutableRef, nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to convert result: %s", err)
		}
		ref, err := cacheRes.SingleRef()
		if err != nil {
			return nil, err
		}

		platform, err := platforms.Parse(platformString)
		if err != nil {
			return nil, err
		}
		cfgBytes, err := json.Marshal(specs.Image{
			Platform: specs.Platform{
				Architecture: platform.Architecture,
				OS:           platform.OS,
				OSVersion:    platform.OSVersion,
				OSFeatures:   platform.OSFeatures,
			},
			Config: input.Config,
		})
		if err != nil {
			return nil, err
		}
		if len(inputByPlatform) == 1 {
			combinedResult.AddMeta(exptypes.ExporterImageConfigKey, cfgBytes)
			combinedResult.SetRef(ref)
		} else {
			combinedResult.AddMeta(fmt.Sprintf("%s/%s", exptypes.ExporterImageConfigKey, platformString), cfgBytes)
			expPlatforms.Platforms = append(expPlatforms.Platforms, exptypes.Platform{
				ID:       platformString,
				Platform: platform,
			})
			combinedResult.AddRef(platformString, ref)
		}
	}
	if len(combinedResult.Refs) > 1 {
		platformBytes, err := json.Marshal(expPlatforms)
		if err != nil {
			return nil, err
		}
		combinedResult.AddMeta(exptypes.ExporterPlatformsKey, platformBytes)
	}

	exporterName := bkclient.ExporterDocker
	if len(combinedResult.Refs) > 1 {
		exporterName = bkclient.ExporterOCI
	}

	exporter, err := c.Worker.Exporter(exporterName, c.SessionManager)
	if err != nil {
		return nil, err
	}

	expInstance, err := exporter.Resolve(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve exporter: %s", err)
	}

	// TODO: workaround needed until upstream fix: https://github.com/moby/buildkit/pull/4049
	// TODO: This probably doesn't entirely work yet in the case where the combined result is still
	// lazy and relies on other session resources to be evaluated. Fix that if merging before upstream
	// fix in place.
	sess, err := c.newFileSendServerProxySession(ctx, destPath)
	if err != nil {
		return nil, err
	}

	resp, descRef, err := expInstance.Export(ctx, combinedResult, sess.ID())
	if err != nil {
		return nil, fmt.Errorf("failed to export: %s", err)
	}
	if descRef != nil {
		descRef.Release()
	}
	return resp, nil
}

func withOutgoingContext(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	return ctx
}
