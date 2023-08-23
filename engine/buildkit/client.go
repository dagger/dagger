package buildkit

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session"
	"github.com/hashicorp/go-multierror"
	bkcache "github.com/moby/buildkit/cache"
	bkcacheconfig "github.com/moby/buildkit/cache/config"
	"github.com/moby/buildkit/cache/remotecache"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
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
	bkworker "github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
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
	DNSConfig        *oci.DNSConfig
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

	dialer *net.Dialer

	closeCtx context.Context
	cancel   context.CancelFunc
	closeMu  sync.RWMutex
}

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

	client.dialer = &net.Dialer{}

	if opts.DNSConfig != nil {
		client.dialer.Resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				if len(opts.DNSConfig.Nameservers) == 0 {
					return nil, errors.New("no nameservers configured")
				}

				var errs error
				for _, ns := range opts.DNSConfig.Nameservers {
					conn, err := client.dialer.DialContext(ctx, network, net.JoinHostPort(ns, "53"))
					if err != nil {
						errs = multierror.Append(errs, err)
						continue
					}

					return conn, nil
				}

				return nil, errs
			},
		}
	}

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

func (c *Client) ResolveImageConfig(ctx context.Context, ref string, opt llb.ResolveImageConfigOpt) (string, digest.Digest, []byte, error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return "", "", nil, err
	}
	defer cancel()
	ctx = withOutgoingContext(ctx)
	return c.llbBridge.ResolveImageConfig(ctx, ref, opt)
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
	if c.containers == nil {
		if err := ctr.Release(context.Background()); err != nil {
			return nil, fmt.Errorf("release after close: %w", err)
		}
		return nil, errors.New("client closed")
	}
	c.containers[ctr] = struct{}{}
	return ctr, nil
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

func (c *Client) ListenHostToContainer(
	ctx context.Context,
	hostListenAddr, proto, upstream string,
) (*session.ListenResponse, func() error, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get requester session ID: %s", err)
	}

	clientCaller, err := c.SessionManager.Get(ctx, clientMetadata.ClientID, false)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get requester session: %s", err)
	}

	conn := clientCaller.Conn()

	tunnelClient := session.NewTunnelListenerClient(conn)

	listener, err := tunnelClient.Listen(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to listen: %s", err)
	}

	err = listener.Send(&session.ListenRequest{
		Addr:     hostListenAddr,
		Protocol: proto,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to send listen request: %s", err)
	}

	listenRes, err := listener.Recv()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to receive listen response: %s", err)
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
		sendL.Lock()
		err := listener.CloseSend()
		sendL.Unlock()
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
