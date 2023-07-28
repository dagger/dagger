package buildkit

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

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
	// TODO: give precise definition
	MainClientCaller bksession.Caller
}

type ResolveCacheExporterFunc func(ctx context.Context, g bksession.Group) (remotecache.Exporter, error)

// Client is dagger's internal interface to buildkit APIs
type Client struct {
	Opts
	session   *bksession.Session
	job       *bksolver.Job
	llbBridge bkfrontend.FrontendLLBBridge

	clientHostnameToID   map[string]string
	clientHostnameToIDMu sync.RWMutex

	refs         map[*ref]struct{}
	refsMu       sync.Mutex
	containers   map[bkgw.Container]struct{}
	containersMu sync.Mutex
}

type Result = solverresult.Result[*ref]

func NewClient(ctx context.Context, opts Opts) (*Client, error) {
	client := &Client{
		Opts:               opts,
		clientHostnameToID: make(map[string]string),
		refs:               make(map[*ref]struct{}),
		containers:         make(map[bkgw.Container]struct{}),
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
	// TODO: ? if you change this, be sure to change the session ID provide to llbBridge methods too
	return c.session.ID()
}

// TODO: Integ test for all cache being releasable at end of every integ test suite
func (c *Client) Close() error {
	c.job.Discard()
	c.job.CloseProgress()

	c.refsMu.Lock()
	for rf := range c.refs {
		if rf != nil {
			rf.resultProxy.Release(context.Background())
		}
	}
	c.refs = nil // TODO: make sure everything else handles this in case there's so other request happening for some reason and we don't get a panic. Or maybe just have a RWMutex for checks whether we have closed everywhere
	c.refsMu.Unlock()

	c.containersMu.Lock()
	for ctr := range c.containers {
		if ctr != nil {
			// TODO: can this block a long time on accident? should we have a timeout?
			ctr.Release(context.Background())
		}
	}
	c.containers = nil // TODO: same as above about handling this
	c.containersMu.Unlock()

	// TODO: ensure session is fully closed by goroutines started in client.newSession

	return nil
}

func (c *Client) Solve(ctx context.Context, req bkgw.SolveRequest) (_ *Result, rerr error) {
	ctx = withOutgoingContext(ctx)

	// include any upstream cache imports, if any
	req.CacheImports = c.UpstreamCacheImports

	llbRes, err := c.llbBridge.Solve(ctx, req, c.ID())
	if err != nil {
		return nil, wrapError(ctx, err, c.ID())
	}
	res, err := solverresult.ConvertResult(llbRes, func(rp bksolver.ResultProxy) (*ref, error) {
		return newRef(rp, c), nil
	})
	if err != nil {
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
	ctx = withOutgoingContext(ctx)
	_, digest, configBytes, err := c.llbBridge.ResolveImageConfig(ctx, ref, opt)
	return digest, configBytes, err
}

func (c *Client) NewContainer(ctx context.Context, req bkgw.NewContainerRequest) (bkgw.Container, error) {
	ctx = withOutgoingContext(ctx)
	ctrReq := bkcontainer.NewContainerRequest{
		ContainerID: identity.NewID(), // TODO: give a meaningful name?
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
	// TODO: this also adds to c.refs... which is weird
	return c.Solve(ctx, bkgw.SolveRequest{
		Definition: llbdef.ToPB(),
	})
}

func (c *Client) UpstreamCacheExport(ctx context.Context, cacheExportFuncs []ResolveCacheExporterFunc) error {
	if len(cacheExportFuncs) == 0 {
		return nil
	}
	// TODO: have caller embed bklog w/ preset fields for client
	bklog.G(ctx).Debugf("exporting %d caches", len(cacheExportFuncs))

	combinedResult, err := c.CombinedResult(ctx) // TODO: lock needed now for this method internally
	if err != nil {
		return err
	}
	// TODO: dedupe with similar conversions
	bklog.G(ctx).Debugf("converting to cacheRes")
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
	// TODO: send progrock statuses
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

func (c *Client) RegisterClient(clientID, clientHostname string) {
	c.clientHostnameToIDMu.Lock()
	defer c.clientHostnameToIDMu.Unlock()
	// TODO: error out if clientID already exists? How would a user accomplish that? They'd need to somehow connect to the same server id
	c.clientHostnameToID[clientHostname] = clientID
}

func (c *Client) DeregisterClientHostname(clientHostname string) {
	c.clientHostnameToIDMu.Lock()
	defer c.clientHostnameToIDMu.Unlock()
	delete(c.clientHostnameToID, clientHostname)
}

func (c *Client) getClientIDByHostname(clientHostname string) (string, error) {
	c.clientHostnameToIDMu.RLock()
	defer c.clientHostnameToIDMu.RUnlock()
	clientID, ok := c.clientHostnameToID[clientHostname]
	if !ok {
		// TODO:
		// return "", fmt.Errorf("client hostname %q not found", clientHostname)
		return "", fmt.Errorf("client hostname %q not found: %s", clientHostname, string(debug.Stack()))
	}
	return clientID, nil
}

type LocalImportOpts struct {
	OwnerClientID string `json:"ownerClientID,omitempty"`
	Path          string `json:"path,omitempty"`
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

	// TODO: double check that reading the client id from the context here is correct, and that it shouldn't
	// instead be deser'd from the local name. I think it's okay provided we still do the local dir import
	// synchronously in the caller of this.
	name, err := EncodeIDHack(LocalImportOpts{
		// For now, the requester is always the owner of the local dir
		// when the dir is initially created in LLB (i.e. you can't request a
		// a new local dir from another session, you can only be passed one
		// from another session already created).
		OwnerClientID: clientMetadata.ClientID,
		Path:          srcPath,
	})
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

func (c *Client) LocalDirExport(
	ctx context.Context,
	def *bksolverpb.Definition,
	destPath string,
) error {
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

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.MD{}
	}
	md[engine.LocalDirExportDestClientIDMetaKey] = []string{clientMetadata.ClientID}
	md[engine.LocalDirExportDestPathMetaKey] = []string{destPath}

	ctx = metadata.NewOutgoingContext(ctx, md)

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

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.MD{}
	}
	md[engine.LocalDirExportDestClientIDMetaKey] = []string{clientMetadata.ClientID}
	md[engine.LocalDirExportDestPathMetaKey] = []string{destPath}
	md[engine.LocalDirExportIsFileStreamMetaKey] = []string{"true"}
	md[engine.LocalDirExportFileOriginalNameMetaKey] = []string{filepath.Base(filePath)}
	if allowParentDirPath {
		md[engine.LocalDirExportAllowParentDirPathMetaKey] = []string{"true"}
	}
	ctx = metadata.NewOutgoingContext(ctx, md)

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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
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

type hostSocketOpts struct {
	HostPath       string `json:"host_path,omitempty"`
	ClientHostname string `json:"client_hostname,omitempty"`
}

func (c *Client) SocketLLBID(hostPath, clientHostname string) (string, error) {
	idBytes, err := json.Marshal(hostSocketOpts{
		HostPath:       hostPath,
		ClientHostname: clientHostname,
	})
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(idBytes), nil
}

func withOutgoingContext(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	return ctx
}
