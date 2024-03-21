package buildkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/koron-go/prefixw"
	bkcache "github.com/moby/buildkit/cache"
	bkcacheconfig "github.com/moby/buildkit/cache/config"
	"github.com/moby/buildkit/cache/remotecache"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/executor"
	"github.com/moby/buildkit/executor/oci"
	bkfrontend "github.com/moby/buildkit/frontend"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bkcontainer "github.com/moby/buildkit/frontend/gateway/container"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	bksecrets "github.com/moby/buildkit/session/secrets"
	bksolver "github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/llbsolver"
	bksolverpb "github.com/moby/buildkit/solver/pb"
	solverresult "github.com/moby/buildkit/solver/result"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/entitlements"
	"github.com/moby/buildkit/util/progress/progressui"
	bkworker "github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/metadata"

	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/engine/session"
)

const (
	// from buildkit, cannot change
	entitlementsJobKey = "llb.entitlements"
)

// Opts for a Client that are shared across all instances for a given DaggerServer
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
	DNSConfig        *oci.DNSConfig
	Frontends        map[string]bkfrontend.Frontend
	BuildkitLogSink  io.Writer
	sharedClientState
}

// these maps are shared across all clients for a given DaggerServer and are
// mutated by each client
type sharedClientState struct {
	execMetadataMu sync.Mutex
	execMetadata   map[digest.Digest]ContainerExecUncachedMetadata
	refsMu         sync.Mutex
	refs           map[*ref]struct{}
}

type ResolveCacheExporterFunc func(ctx context.Context, g bksession.Group) (remotecache.Exporter, error)

// Client is dagger's internal interface to buildkit APIs
type Client struct {
	*Opts

	spanCtx trace.SpanContext

	session   *bksession.Session
	job       *bksolver.Job
	llbBridge bkfrontend.FrontendLLBBridge
	llbExec   executor.Executor

	containers   map[bkgw.Container]struct{}
	containersMu sync.Mutex

	dialer *net.Dialer

	closeCtx context.Context
	cancel   context.CancelFunc
	closeMu  sync.RWMutex
}

func NewClient(ctx context.Context, opts *Opts) (*Client, error) {
	// override the outer cancel, we will manage cancellation ourselves here
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	client := &Client{
		Opts:       opts,
		spanCtx:    trace.SpanContextFromContext(ctx),
		containers: make(map[bkgw.Container]struct{}),
		closeCtx:   ctx,
		cancel:     cancel,
	}

	// initialize ref+metadata caches if needed
	client.refsMu.Lock()
	if client.refs == nil {
		client.refs = make(map[*ref]struct{})
	}
	client.refsMu.Unlock()
	client.execMetadataMu.Lock()
	if client.execMetadata == nil {
		client.execMetadata = make(map[digest.Digest]ContainerExecUncachedMetadata)
	}
	client.execMetadataMu.Unlock()

	session, err := client.newSession()
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

	// TODO: upstream Bridge should return an executor
	br := client.LLBSolver.Bridge(client.job).(interface {
		bkfrontend.FrontendLLBBridge
		executor.Executor
	})
	gw := &opTrackingGateway{llbBridge: br}
	client.llbBridge = gw
	client.llbExec = br

	client.dialer = &net.Dialer{}

	if opts.DNSConfig != nil {
		client.dialer.Resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				if len(opts.DNSConfig.Nameservers) == 0 {
					return nil, errors.New("no nameservers configured")
				}

				var errs []error
				for _, ns := range opts.DNSConfig.Nameservers {
					conn, err := client.dialer.DialContext(ctx, network, net.JoinHostPort(ns, "53"))
					if err != nil {
						errs = append(errs, err)
						continue
					}

					return conn, nil
				}

				return nil, errors.Join(errs...)
			},
		}
	}

	bkLogsW := opts.BuildkitLogSink
	if bkLogsW == nil {
		bkLogsW = io.Discard
	}
	go client.WriteStatusesTo(ctx, bkLogsW)

	return client, nil
}

func (c *Client) WriteStatusesTo(ctx context.Context, dest io.Writer) {
	prefix := fmt.Sprintf("[buildkit] [trace=%s] [client=%s] ", c.spanCtx.TraceID(), c.ID())
	dest = prefixw.New(dest, prefix)
	statusCh := make(chan *bkclient.SolveStatus, 8)
	pw, err := progressui.NewDisplay(dest, progressui.PlainMode)
	if err != nil {
		bklog.G(ctx).WithError(err).Error("failed to initialize progress writer")
		return
	}
	go pw.UpdateFrom(ctx, statusCh)
	err = c.job.Status(ctx, statusCh)
	if err != nil && !errors.Is(err, context.Canceled) {
		bklog.G(ctx).WithError(err).Error("failed to write status updates")
	}
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

	// include exec metadata that isn't included in the cache key
	var llbRes *bkfrontend.Result
	switch {
	case req.Definition != nil && req.Definition.Def != nil:
		llbRes, err = c.llbBridge.Solve(ctx, req, c.ID())
		if err != nil {
			return nil, wrapError(ctx, err, c.ID())
		}
	case req.Frontend != "":
		// HACK: don't force evaluation like this, we can write custom frontend
		// wrappers (for dockerfile.v0 and gateway.v0) that read from ctx to
		// replace the llbBridge it knows about.
		// This current implementation may be limited when it comes to
		// implement provenance/etc.

		f, ok := c.Frontends[req.Frontend]
		if !ok {
			return nil, fmt.Errorf("invalid frontend: %s", req.Frontend)
		}

		gw := newFilterGateway(c.llbBridge, req)
		gw.secretTranslator = ctx.Value("secret-translator").(func(string) (string, error))

		llbRes, err = f.Solve(ctx, gw, c.llbExec, req.FrontendOpt, req.FrontendInputs, c.ID(), c.SessionManager)
		if err != nil {
			return nil, err
		}
		if req.Evaluate {
			err = llbRes.EachRef(func(ref bksolver.ResultProxy) error {
				_, err := ref.Result(ctx)
				return err
			})
			if err != nil {
				return nil, err
			}
		}
	default:
		llbRes = &bkfrontend.Result{}
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

func (c *Client) ResolveImageConfig(ctx context.Context, ref string, opt sourceresolver.Opt) (string, digest.Digest, []byte, error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return "", "", nil, err
	}
	defer cancel()
	ctx = withOutgoingContext(ctx)

	imr := sourceresolver.NewImageMetaResolver(c.llbBridge)
	return imr.ResolveImageConfig(ctx, ref, opt)
}

func (c *Client) ResolveSourceMetadata(ctx context.Context, op *bksolverpb.SourceOp, opt sourceresolver.Opt) (*sourceresolver.MetaResponse, error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	ctx = withOutgoingContext(ctx)

	return c.llbBridge.ResolveSourceMetadata(ctx, op, opt)
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
			var workerRef *bkworker.WorkerRef
			if m.Ref != nil {
				ref, ok := m.Ref.(*ref)
				if !ok {
					return fmt.Errorf("dagger: unexpected ref type: %T", m.Ref)
				}
				if ref != nil { // TODO(vito): apparently this is possible. scratch?
					res, err := ref.resultProxy.Result(egctx)
					if err != nil {
						return fmt.Errorf("result: %w", err)
					}
					workerRef, ok = res.Sys().(*bkworker.WorkerRef)
					if !ok {
						return fmt.Errorf("invalid res: %T", res.Sys())
					}
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
		return nil, fmt.Errorf("wait: %w", err)
	}

	// using context.Background so it continues running until exit or when c.Close() is called
	ctr, err := bkcontainer.NewContainer(
		context.Background(),
		c.Worker.CacheManager(),
		c.llbExec,
		c.SessionManager,
		bksession.NewGroup(c.ID()),
		ctrReq,
	)
	if err != nil {
		return nil, err
	}

	c.containersMu.Lock()
	defer c.containersMu.Unlock()
	if c.containers == nil {
		if err := ctr.Release(context.Background()); err != nil {
			return nil, fmt.Errorf("release after close: %w", err)
		}
		return nil, errors.New("client closed")
	}
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
	cacheRes, err := ConvertToWorkerCacheResult(ctx, combinedResult)
	if err != nil {
		return fmt.Errorf("failed to convert result: %w", err)
	}
	bklog.G(ctx).Debugf("converting to solverRes")
	solverRes, err := solverresult.ConvertResult(combinedResult, func(rf *ref) (bksolver.CachedResult, error) {
		return rf.resultProxy.Result(ctx)
	})
	if err != nil {
		return fmt.Errorf("failed to convert result: %w", err)
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

func (c *Client) ListenHostToContainer(
	ctx context.Context,
	hostListenAddr, proto, upstream string,
) (*session.ListenResponse, func() error, error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, nil, err
	}

	clientCaller, err := c.GetSessionCaller(ctx, false)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("failed to get requester session: %w", err)
	}

	conn := clientCaller.Conn()

	tunnelClient := session.NewTunnelListenerClient(conn)

	listener, err := tunnelClient.Listen(ctx)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("failed to listen: %w", err)
	}

	err = listener.Send(&session.ListenRequest{
		Addr:     hostListenAddr,
		Protocol: proto,
	})
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("failed to send listen request: %w", err)
	}

	listenRes, err := listener.Recv()
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("failed to receive listen response: %w", err)
	}

	conns := map[string]net.Conn{}
	connsL := &sync.Mutex{}
	sendL := &sync.Mutex{}

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			res, err := listener.Recv()
			if err != nil {
				bklog.G(ctx).Warnf("listener recv err: %s", err)
				return
			}

			connID := res.GetConnId()
			if connID == "" {
				continue
			}

			connsL.Lock()
			conn, found := conns[connID]
			connsL.Unlock()

			if !found {
				conn, err := c.dialer.Dial(proto, upstream)
				if err != nil {
					bklog.G(ctx).Warnf("failed to dial %s %s: %s", proto, upstream, err)
					return
				}

				connsL.Lock()
				conns[connID] = conn
				connsL.Unlock()

				wg.Add(1)
				go func() {
					defer wg.Done()

					data := make([]byte, 32*1024)
					for {
						n, err := conn.Read(data)
						if err != nil {
							return
						}

						sendL.Lock()
						err = listener.Send(&session.ListenRequest{
							ConnId: connID,
							Data:   data[:n],
						})
						sendL.Unlock()
						if err != nil {
							return
						}
					}
				}()
			}

			if res.Data != nil {
				_, err = conn.Write(res.Data)
				if err != nil {
					return
				}
			}
		}
	}()

	return listenRes, func() error {
		defer cancel()
		sendL.Lock()
		err := listener.CloseSend()
		sendL.Unlock()
		connsL.Lock()
		for _, conn := range conns {
			conn.Close()
		}
		clear(conns)
		connsL.Unlock()
		if err == nil {
			wg.Wait()
		}
		return err
	}, nil
}

func withOutgoingContext(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	return ctx
}

// Metadata passed to an exec that doesn't count towards the cache key.
// This should be used with great caution; only for metadata that is
// safe to be de-duplicated across execs.
//
// Currently, this uses the FTPProxy LLB option to pass without becoming
// part of the cache key. This is a hack that, while ugly to look at,
// is simple and robust. Alternatives would be to use secrets or sockets,
// but they are more complicated, or to create a custom buildkit
// worker/executor, which is MUCH more complicated.
//
// If a need to add ftp proxy support arises, then we can just also embed
// the "real" ftp proxy setting in here too and have the shim handle
// leaving only that set in the actual env var.
type ContainerExecUncachedMetadata struct {
	ParentClientIDs []string `json:"parentClientIDs,omitempty"`
	ServerID        string   `json:"serverID,omitempty"`
}

func (md ContainerExecUncachedMetadata) ToPBFtpProxyVal() (string, error) {
	b, err := json.Marshal(md)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (md *ContainerExecUncachedMetadata) FromEnv(envKV string) (bool, error) {
	_, val, ok := strings.Cut(envKV, "ftp_proxy=")
	if !ok {
		return false, nil
	}
	err := json.Unmarshal([]byte(val), md)
	if err != nil {
		return false, err
	}
	return true, nil
}

// filteringGateway is a helper gateway that filters+converts various
// operations for the frontend
type filteringGateway struct {
	bkfrontend.FrontendLLBBridge

	// secretTranslator is a function to convert secret ids. Frontends may
	// attempt to access secrets by raw IDs, but they may be keyed differently
	// in the secret store.
	secretTranslator func(string) (string, error)

	// skipInputs specifies op digests that were part of the request inputs and
	// so shouldn't be processed.
	skipInputs map[digest.Digest]struct{}
}

func newFilterGateway(bridge bkfrontend.FrontendLLBBridge, req bkgw.SolveRequest) *filteringGateway {
	inputs := map[digest.Digest]struct{}{}
	for _, inp := range req.FrontendInputs {
		for _, def := range inp.Def {
			inputs[digest.FromBytes(def)] = struct{}{}
		}
	}

	return &filteringGateway{
		FrontendLLBBridge: bridge,
		skipInputs:        inputs,
	}
}

func (gw *filteringGateway) Solve(ctx context.Context, req bkfrontend.SolveRequest, sid string) (*bkfrontend.Result, error) {
	if req.Definition != nil && req.Definition.Def != nil {
		dag, err := DefToDAG(req.Definition)
		if err != nil {
			return nil, err
		}
		if err := dag.Walk(func(dag *OpDAG) error {
			if _, ok := gw.skipInputs[*dag.OpDigest]; ok {
				return SkipInputs
			}

			execOp, ok := dag.AsExec()
			if !ok {
				return nil
			}

			for _, secret := range execOp.ExecOp.GetSecretenv() {
				secret.ID, err = gw.secretTranslator(secret.ID)
				if err != nil {
					return err
				}
			}
			for _, mount := range execOp.ExecOp.GetMounts() {
				if mount.MountType != bksolverpb.MountType_SECRET {
					continue
				}
				secret := mount.SecretOpt
				secret.ID, err = gw.secretTranslator(secret.ID)
				if err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}

		newDef, err := dag.Marshal()
		if err != nil {
			return nil, err
		}
		req.Definition = newDef
	}

	return gw.FrontendLLBBridge.Solve(ctx, req, sid)
}
