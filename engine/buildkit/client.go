package buildkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	contentapi "github.com/containerd/containerd/api/services/content/v1"
	imagesapi "github.com/containerd/containerd/api/services/images/v1"
	leasesapi "github.com/containerd/containerd/api/services/leases/v1"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	contentproxy "github.com/containerd/containerd/v2/core/content/proxy"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	leasesproxy "github.com/containerd/containerd/v2/core/leases/proxy"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkcacheconfig "github.com/dagger/dagger/internal/buildkit/cache/config"
	"github.com/dagger/dagger/internal/buildkit/cache/remotecache"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/dagger/dagger/internal/buildkit/client/llb/sourceresolver"
	bkfrontend "github.com/dagger/dagger/internal/buildkit/frontend"
	bkgw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	bksolver "github.com/dagger/dagger/internal/buildkit/solver"
	bksolverpb "github.com/dagger/dagger/internal/buildkit/solver/pb"
	solverresult "github.com/dagger/dagger/internal/buildkit/solver/result"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/internal/buildkit/util/entitlements"
	bkworker "github.com/dagger/dagger/internal/buildkit/worker"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/engine/session/h2c"
	"github.com/dagger/dagger/engine/session/pipe"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/dagger/dagger/engine/session/store"
	"github.com/dagger/dagger/engine/session/terminal"
)

const (
	// from buildkit, cannot change
	EntitlementsJobKey = "llb.entitlements"

	// OCIStoreName is the name of the OCI content store used for OCI tarball
	// imports.
	OCIStoreName = "dagger-oci"

	// BuiltinContentOCIStoreName is the name of the OCI content store used for
	// builtins like SDKs that we package with the engine container but still use
	// in LLB.
	BuiltinContentOCIStoreName = "dagger-builtin-content"
)

// Opts for a Client that are shared across all instances for a given DaggerServer
type Opts struct {
	Worker               *Worker
	SessionManager       *bksession.Manager
	BkSession            *bksession.Session
	LLBBridge            bkfrontend.FrontendLLBBridge
	Dialer               *net.Dialer
	GetClientCaller      func(string) (bksession.Caller, error)
	GetMainClientCaller  func() (bksession.Caller, error)
	Entitlements         entitlements.Set
	UpstreamCacheImports []bkgw.CacheOptionsEntry
	Frontends            map[string]bkfrontend.Frontend

	Refs   map[Reference]struct{}
	RefsMu *sync.Mutex

	Interactive        bool
	InteractiveCommand []string

	ParentClient *Client
}

type ResolveCacheExporterFunc func(ctx context.Context, g bksession.Group) (remotecache.Exporter, error)

// Client is dagger's internal interface to buildkit APIs
type Client struct {
	*Opts

	closeCtx context.Context
	cancel   context.CancelCauseFunc
	closeMu  sync.RWMutex
	execMap  sync.Map

	ops   map[digest.Digest]opCtx
	opsmu sync.RWMutex
}

type opCtx struct {
	od  *OpDAG
	ctx trace.SpanContext
}

func NewClient(ctx context.Context, opts *Opts) (*Client, error) {
	// override the outer cancel, we will manage cancellation ourselves here
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	client := &Client{
		Opts:     opts,
		closeCtx: ctx,
		cancel:   cancel,
		execMap:  sync.Map{},
		ops:      make(map[digest.Digest]opCtx),
	}

	return client, nil
}

func (c *Client) ID() string {
	return c.BkSession.ID()
}

func (c *Client) withClientCloseCancel(ctx context.Context) (context.Context, context.CancelCauseFunc, error) {
	c.closeMu.RLock()
	defer c.closeMu.RUnlock()
	select {
	case <-c.closeCtx.Done():
		return nil, nil, errors.New("client closed")
	default:
	}
	ctx, cancel := context.WithCancelCause(ctx)
	go func() {
		select {
		case <-c.closeCtx.Done():
			cancel(context.Cause(c.closeCtx))
		case <-ctx.Done():
		}
	}()
	return ctx, cancel, nil
}

func (c *Client) getRootClient() *Client {
	if c.ParentClient == nil {
		return c
	}
	return c.ParentClient.getRootClient()
}

func (c *Client) Solve(ctx context.Context, req bkgw.SolveRequest) (_ *Result, rerr error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel(errors.New("solve done"))
	ctx = withOutgoingContext(ctx)

	recordOp := func(def *bksolverpb.Definition) error {
		dag, err := DefToDAG(def)
		if err != nil {
			return err
		}
		spanCtx := trace.SpanContextFromContext(ctx)
		rootClient := c.getRootClient()
		rootClient.opsmu.Lock()
		_ = dag.Walk(func(od *OpDAG) error {
			rootClient.ops[*od.OpDigest] = opCtx{
				od:  od,
				ctx: spanCtx,
			}
			return nil
		})
		rootClient.opsmu.Unlock()
		return nil
	}
	if req.Definition != nil {
		if err := recordOp(req.Definition); err != nil {
			return nil, fmt.Errorf("record def ops: %w", err)
		}
	}
	for name, def := range req.FrontendInputs {
		if err := recordOp(def); err != nil {
			return nil, fmt.Errorf("record frontend input %s ops: %w", name, err)
		}
	}

	// include upstream cache imports, if any
	req.CacheImports = c.UpstreamCacheImports

	// handle secret translation
	gw := newFilterGateway(c, req)
	if v := SecretTranslatorFromContext(ctx); v != nil {
		gw.secretTranslator = v
	}
	llbRes, err := gw.Solve(ctx, req, c.ID())
	if err != nil {
		return nil, WrapError(ctx, err, c)
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

	c.RefsMu.Lock()
	defer c.RefsMu.Unlock()
	if res.Ref != nil {
		c.Refs[res.Ref] = struct{}{}
	}
	for _, rf := range res.Refs {
		c.Refs[rf] = struct{}{}
	}
	return res, nil
}

func (c *Client) LookupOp(vertex digest.Digest) (*OpDAG, trace.SpanContext, bool) {
	c = c.getRootClient()
	c.opsmu.Lock()
	opCtx, ok := c.ops[vertex]
	c.opsmu.Unlock()
	return opCtx.od, opCtx.ctx, ok
}

func (c *Client) ResolveImageConfig(ctx context.Context, ref string, opt sourceresolver.Opt) (string, digest.Digest, []byte, error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return "", "", nil, err
	}
	defer cancel(errors.New("resolve image config done"))
	ctx = withOutgoingContext(ctx)

	imr := sourceresolver.NewImageMetaResolver(c.LLBBridge)
	return imr.ResolveImageConfig(ctx, ref, opt)
}

func (c *Client) ResolveSourceMetadata(ctx context.Context, op *bksolverpb.SourceOp, opt sourceresolver.Opt) (*sourceresolver.MetaResponse, error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel(errors.New("resolve source metadata done"))
	ctx = withOutgoingContext(ctx)

	return c.LLBBridge.ResolveSourceMetadata(ctx, op, opt)
}

func (c *Client) NewNetworkNamespace(ctx context.Context, hostname string) (Namespaced, error) {
	return c.Worker.newNetNS(ctx, hostname)
}

func RunInNetNS[T any](
	ctx context.Context,
	c *Client,
	ns Namespaced,
	fn func() (T, error),
) (T, error) {
	var zero T
	if c == nil {
		return zero, errors.New("client is nil")
	}
	if ns == nil {
		return zero, errors.New("namespace is nil")
	}

	c.Worker.mu.RLock()
	runState, ok := c.Worker.running[ns.NamespaceID()]
	c.Worker.mu.RUnlock()
	if !ok {
		return zero, fmt.Errorf("namespace for %s not found in running state", ns.NamespaceID())
	}

	return runInNetNS(ctx, runState, fn)
}

// CombinedResult returns a buildkit result with all the refs solved by this client so far.
// This is useful for constructing a result for upstream remote caching.
func (c *Client) CombinedResult(ctx context.Context) (*Result, error) {
	c.RefsMu.Lock()
	mergeInputs := make([]llb.State, 0, len(c.Refs))
	for r := range c.Refs {
		state, err := r.ToState()
		if err != nil {
			c.RefsMu.Unlock()
			return nil, err
		}
		mergeInputs = append(mergeInputs, state)
	}
	c.RefsMu.Unlock()
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
	defer cancel(errors.New("upstream cache export done"))

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
	return bksolver.WithCacheOptGetter(ctx, func(_ bool, keys ...any) map[any]any {
		vals := make(map[any]any)
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

func (c *Client) GetSessionCaller(ctx context.Context, wait bool) (_ bksession.Caller, rerr error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	bklog.G(ctx).Tracef("getting session for %q", clientMetadata.ClientID)
	defer func() {
		bklog.G(ctx).WithError(rerr).Tracef("got session for %q", clientMetadata.ClientID)
	}()

	caller, err := c.SessionManager.Get(ctx, clientMetadata.ClientID, !wait)
	if err != nil {
		return nil, err
	}
	if caller == nil {
		return nil, fmt.Errorf("session for %q not found", clientMetadata.ClientID)
	}
	return caller, nil
}

func (c *Client) ListenHostToContainer(
	ctx context.Context,
	hostListenAddr, proto, upstream string,
) (*h2c.ListenResponse, func() error, error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, nil, err
	}

	clientCaller, err := c.GetSessionCaller(ctx, false)
	if err != nil {
		err = fmt.Errorf("failed to get requester session: %w", err)
		cancel(fmt.Errorf("listen host to container error: %w", err))
		return nil, nil, err
	}

	conn := clientCaller.Conn()

	tunnelClient := h2c.NewTunnelListenerClient(conn)

	listener, err := tunnelClient.Listen(ctx)
	if err != nil {
		err = fmt.Errorf("failed to listen: %w", err)
		cancel(fmt.Errorf("listen host to container error: %w", err))
		return nil, nil, err
	}

	err = listener.Send(&h2c.ListenRequest{
		Addr:     hostListenAddr,
		Protocol: proto,
	})
	if err != nil {
		err = fmt.Errorf("failed to send listen request: %w", err)
		cancel(fmt.Errorf("listen host to container error: %w", err))
		return nil, nil, err
	}

	listenRes, err := listener.Recv()
	if err != nil {
		err = fmt.Errorf("failed to receive listen response: %w", err)
		cancel(fmt.Errorf("listen host to container error: %w", err))
		return nil, nil, err
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
				conn, err = c.Dialer.Dial(proto, upstream)
				if err != nil {
					bklog.G(ctx).Warnf("failed to dial %s %s: %s", proto, upstream, err)
					sendL.Lock()
					err = listener.Send(&h2c.ListenRequest{
						ConnId: connID,
						Close:  true,
					})
					sendL.Unlock()
					if err != nil {
						return
					}
					continue
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
							break
						}

						sendL.Lock()
						err = listener.Send(&h2c.ListenRequest{
							ConnId: connID,
							Data:   data[:n],
						})
						sendL.Unlock()
						if err != nil {
							break
						}
					}

					sendL.Lock()
					_ = listener.Send(&h2c.ListenRequest{
						ConnId: connID,
						Close:  true,
					})
					sendL.Unlock()
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
		defer cancel(errors.New("listen host to container done"))
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

func (c *Client) GetCredential(ctx context.Context, protocol, host, path string) (*git.CredentialInfo, error) {
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := c.GetClientCaller(md.ClientID)
	if err != nil {
		return nil, fmt.Errorf("failed to get client caller for %q: %w", md.ClientID, err)
	}

	response, err := git.NewGitClient(caller.Conn()).GetCredential(ctx, &git.GitCredentialRequest{
		Protocol: protocol,
		Host:     host,
		Path:     path,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query credentials: %w", err)
	}

	switch result := response.Result.(type) {
	case *git.GitCredentialResponse_Credential:
		return result.Credential, nil
	case *git.GitCredentialResponse_Error:
		return nil, fmt.Errorf("git credential error: %s", result.Error.Message)
	default:
		return nil, fmt.Errorf("unexpected response type")
	}
}

func (c *Client) PromptAllowLLM(ctx context.Context, moduleRepoURL string) error {
	// the flag hasn't allowed this LLM call, so prompt the user
	caller, err := c.GetMainClientCaller()
	if err != nil {
		return fmt.Errorf("failed to get main client caller to prompt for allow llm: %w", err)
	}

	response, err := prompt.NewPromptClient(caller.Conn()).PromptBool(ctx, &prompt.BoolRequest{
		Title:         "Allow LLM access?",
		Prompt:        fmt.Sprintf("Remote module **%s** attempted to access the LLM API. Allow it?", moduleRepoURL),
		PersistentKey: "allow_llm:" + moduleRepoURL,
		Default:       false, // TODO: default to true?
	})
	if err != nil {
		return fmt.Errorf("failed to prompt user for LLM API access from %s: %w", moduleRepoURL, err)
	}
	if response.Response {
		return nil
	}

	return fmt.Errorf("module %s was denied LLM access; pass --allow-llm=%s or --allow-llm=all to allow", moduleRepoURL, moduleRepoURL)
}

func (c *Client) PromptHumanHelp(ctx context.Context, title, question string) (string, error) {
	caller, err := c.GetMainClientCaller()
	if err != nil {
		return "", fmt.Errorf("failed to get main client caller to prompt user for human help: %w", err)
	}

	response, err := prompt.NewPromptClient(caller.Conn()).PromptString(ctx, &prompt.StringRequest{
		Title:   title,
		Prompt:  question,
		Default: "The user did not respond.",
	})
	if err != nil {
		return "", fmt.Errorf("failed to prompt user for human help: %w", err)
	}
	return response.Response, nil
}

func (c *Client) GetGitConfig(ctx context.Context) ([]*git.GitConfigEntry, error) {
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := c.GetClientCaller(md.ClientID)
	if err != nil {
		return nil, fmt.Errorf("failed to get client caller for %q: %w", md.ClientID, err)
	}

	response, err := git.NewGitClient(caller.Conn()).GetConfig(ctx, &git.GitConfigRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to query git config: %w", err)
	}

	switch result := response.Result.(type) {
	case *git.GitConfigResponse_Config:
		return result.Config.Entries, nil
	case *git.GitConfigResponse_Error:
		// if git is not found, ignore that error
		if result.Error.Type == git.NOT_FOUND {
			return []*git.GitConfigEntry{}, nil
		}

		return nil, fmt.Errorf("git config error: %s", result.Error.Message)
	default:
		return nil, fmt.Errorf("unexpected response type")
	}
}

type TerminalClient struct {
	Stdin    io.ReadCloser
	Stdout   io.WriteCloser
	Stderr   io.WriteCloser
	ResizeCh chan bkgw.WinSize
	ErrCh    chan error
	Close    func(exitCode int) error
}

func (c *Client) OpenTerminal(
	ctx context.Context,
) (*TerminalClient, error) {
	caller, err := c.GetMainClientCaller()
	if err != nil {
		return nil, fmt.Errorf("failed to get main client caller: %w", err)
	}
	terminalClient := terminal.NewTerminalClient(caller.Conn())

	term, err := terminalClient.Session(ctx)
	if err != nil {
		// NOTE: confusingly, this starting a stream doesn't actually wait for
		// the response, so the above call can succeed even if the client
		// terminal startup fails immediately
		return nil, fmt.Errorf("failed to open terminal: %w", err)
	}

	var (
		stdoutR, stdoutW = io.Pipe()
		stderrR, stderrW = io.Pipe()
		stdinR, stdinW   = io.Pipe()
	)

	forwardFD := func(r io.ReadCloser, fn func([]byte) *terminal.SessionRequest) error {
		defer r.Close()
		b := make([]byte, 2048)
		for {
			n, err := r.Read(b)
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
					return nil
				}
				return fmt.Errorf("error reading fd: %w", err)
			}

			if err := term.Send(fn(b[:n])); err != nil {
				return fmt.Errorf("error forwarding fd: %w", err)
			}
		}
	}

	go forwardFD(stdoutR, func(stdout []byte) *terminal.SessionRequest {
		return &terminal.SessionRequest{
			Msg: &terminal.SessionRequest_Stdout{Stdout: stdout},
		}
	})

	go forwardFD(stderrR, func(stderr []byte) *terminal.SessionRequest {
		return &terminal.SessionRequest{
			Msg: &terminal.SessionRequest_Stderr{Stderr: stderr},
		}
	})

	resizeCh := make(chan bkgw.WinSize, 1)

	// make sure we can handle *one* message before we start
	// we need to do this, so we don't end up returning an invalid terminal
	res, err := term.Recv()
	if err != nil {
		return nil, fmt.Errorf("failed to open terminal: %w", err)
	}

	switch msg := res.GetMsg().(type) {
	case *terminal.SessionResponse_Ready:
	case *terminal.SessionResponse_Resize:
		// FIXME: only here to handle the first message from olde clients that
		// don't sent a ready message
		resizeCh <- bkgw.WinSize{
			Rows: uint32(msg.Resize.Height),
			Cols: uint32(msg.Resize.Width),
		}
	default:
	}

	errCh := make(chan error, 1)
	go func() {
		defer stdinW.Close()
		defer close(errCh)
		defer close(resizeCh)
		for {
			res, err := term.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					bklog.G(ctx).Warnf("terminal recv err: %v", err)
					errCh <- err
				}
				return
			}
			switch msg := res.GetMsg().(type) {
			case *terminal.SessionResponse_Stdin:
				_, err := stdinW.Write(msg.Stdin)
				if err != nil {
					bklog.G(ctx).Warnf("failed to write stdin: %v", err)
					errCh <- err
					return
				}
			case *terminal.SessionResponse_Resize:
				resizeCh <- bkgw.WinSize{
					Rows: uint32(msg.Resize.Height),
					Cols: uint32(msg.Resize.Width),
				}
			default:
			}
		}
	}()

	return &TerminalClient{
		Stdin:    stdinR,
		Stdout:   stdoutW,
		Stderr:   stderrW,
		ErrCh:    errCh,
		ResizeCh: resizeCh,
		Close: onceValueWithArg(func(exitCode int) error {
			defer stdinR.Close()
			defer stdoutW.Close()
			defer stderrW.Close()
			defer term.CloseSend()

			err := term.Send(&terminal.SessionRequest{
				Msg: &terminal.SessionRequest_Exit{Exit: int32(exitCode)},
			})
			if err != nil {
				return fmt.Errorf("failed to close terminal: %w", err)
			}
			return nil
		}),
	}, nil
}

func (c *Client) OpenPipe(
	ctx context.Context,
) (io.ReadWriteCloser, error) {
	caller, err := c.GetMainClientCaller()
	if err != nil {
		return nil, fmt.Errorf("failed to get main client caller: %w", err)
	}

	// grpc service client
	pipeIOClient, err := pipe.NewPipeClient(caller.Conn()).IO(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open pipe: %w", err)
	}
	// io.ReadWriter wrapper
	return &pipe.PipeIO{GRPC: pipeIOClient}, nil
}

func (c *Client) WriteImage(
	ctx context.Context,
	name string,
) (*ImageWriter, error) {
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := c.GetClientCaller(md.ClientID)
	if err != nil {
		return nil, fmt.Errorf("failed to get client caller: %w", err)
	}

	if callerSupports(caller, &contentapi.Content_ServiceDesc) {
		return &ImageWriter{
			ContentStore: contentproxy.NewContentStore(contentapi.NewContentClient(caller.Conn())),
			ImagesStore:  containerd.NewImageStoreFromClient(imagesapi.NewImagesClient(caller.Conn())),
			LeaseManager: leasesproxy.NewLeaseManager(leasesapi.NewLeasesClient(caller.Conn())),
		}, nil
	}

	if callerSupports(caller, &store.BasicStore_serviceDesc) {
		loadClient := store.NewBasicStoreClient(caller.Conn())
		ctx = metadata.AppendToOutgoingContext(ctx, store.ImageTagKey, name)
		tarballWriter, err := loadClient.WriteTarball(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to open tarball pipe: %w", err)
		}
		return &ImageWriter{
			Tarball: &store.TarballWriter{
				SendF: tarballWriter.Send,
				CloseF: func() error {
					_, err := tarballWriter.CloseAndRecv()
					return err
				},
			},
		}, nil
	}

	return nil, fmt.Errorf("client has no supported api for loading image")
}

func (c *Client) ReadImage(
	ctx context.Context,
	name string,
) (*ImageReader, error) {
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := c.GetClientCaller(md.ClientID)
	if err != nil {
		return nil, fmt.Errorf("failed to get client caller: %w", err)
	}

	if callerSupports(caller, &contentapi.Content_ServiceDesc) {
		return &ImageReader{
			ContentStore: contentproxy.NewContentStore(contentapi.NewContentClient(caller.Conn())),
			ImagesStore:  containerd.NewImageStoreFromClient(imagesapi.NewImagesClient(caller.Conn())),
			LeaseManager: leasesproxy.NewLeaseManager(leasesapi.NewLeasesClient(caller.Conn())),
		}, nil
	}

	if callerSupports(caller, &store.BasicStore_serviceDesc) {
		loadClient := store.NewBasicStoreClient(caller.Conn())
		ctx = metadata.AppendToOutgoingContext(ctx, store.ImageTagKey, name)
		tarballReader, err := loadClient.ReadTarball(ctx, &emptypb.Empty{})
		if err != nil {
			return nil, fmt.Errorf("failed to open tarball pipe: %w", err)
		}
		return &ImageReader{
			Tarball: &store.TarballReader{
				ReadF:  tarballReader.Recv,
				CloseF: tarballReader.CloseSend,
			},
		}, nil
	}

	return nil, fmt.Errorf("client has no supported api for loading image")
}

func callerSupports(caller bksession.Caller, desc *grpc.ServiceDesc) bool {
	for _, method := range desc.Methods {
		if !caller.Supports(fmt.Sprintf("/%s/%s", desc.ServiceName, method.MethodName)) {
			return false
		}
	}
	for _, stream := range desc.Streams {
		if !caller.Supports(fmt.Sprintf("/%s/%s", desc.ServiceName, stream.StreamName)) {
			return false
		}
	}
	return true
}

type ImageWriter struct {
	Tarball io.WriteCloser

	ContentStore content.Store
	ImagesStore  images.Store
	LeaseManager leases.Manager
}

type ImageReader struct {
	Tarball io.ReadCloser

	ContentStore content.Store
	ImagesStore  images.Store
	LeaseManager leases.Manager
}

// like sync.OnceValue but accepts an arg
func onceValueWithArg[A any, R any](f func(A) R) func(A) R {
	var (
		once   sync.Once
		valid  bool
		p      any
		result R
	)
	g := func(a A) {
		defer func() {
			p = recover()
			if !valid {
				panic(p)
			}
		}()
		result = f(a)
		valid = true
	}
	return func(a A) R {
		once.Do(func() {
			g(a)
		})
		if !valid {
			panic(p)
		}
		return result
	}
}

func withOutgoingContext(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	ctx = buildkitTelemetryProvider(ctx)
	return ctx
}

// filteringGateway is a helper gateway that filters+converts various
// operations for the frontend
type filteringGateway struct {
	bkfrontend.FrontendLLBBridge

	// secretTranslator is a function to convert secret ids. Frontends may
	// attempt to access secrets by raw IDs, but they may be keyed differently
	// in the secret store.
	secretTranslator SecretTranslator

	// client is the top-most client that is owning the filtering process
	client *Client

	// skipInputs specifies op digests that were part of the request inputs and
	// so shouldn't be processed.
	skipInputs map[digest.Digest]struct{}
}

func newFilterGateway(client *Client, req bkgw.SolveRequest) *filteringGateway {
	inputs := map[digest.Digest]struct{}{}
	for _, inp := range req.FrontendInputs {
		for _, def := range inp.Def {
			inputs[digest.FromBytes(def)] = struct{}{}
		}
	}

	return &filteringGateway{
		client:            client,
		FrontendLLBBridge: client.LLBBridge,

		skipInputs: inputs,
	}
}

func (gw *filteringGateway) Solve(ctx context.Context, req bkfrontend.SolveRequest, sid string) (*bkfrontend.Result, error) {
	switch {
	case req.Definition != nil && req.Definition.Def != nil:
		if gw.secretTranslator != nil {
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
					secret.ID, err = gw.secretTranslator(secret.ID, secret.Optional)
					if err != nil {
						return err
					}
				}
				for _, mount := range execOp.ExecOp.GetMounts() {
					if mount.MountType != bksolverpb.MountType_SECRET {
						continue
					}
					secret := mount.SecretOpt
					secret.ID, err = gw.secretTranslator(secret.ID, secret.Optional)
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

		res, err := gw.FrontendLLBBridge.Solve(ctx, req, sid)
		if err != nil {
			// writing log w/ %+v so that we can see stack traces embedded in err by buildkit's usage of pkg/errors
			bklog.G(ctx).Errorf("solve error: %+v", err)
			err = includeBuildkitContextCancelledLine(err)
			return nil, err
		}
		return res, nil

	case req.Frontend != "":
		// HACK: don't force evaluation like this, we can write custom frontend
		// wrappers (for dockerfile.v0 and gateway.v0) that read from ctx to
		// replace the llbBridge it knows about.
		// This current implementation may be limited when it comes to
		// implement provenance/etc.

		f, ok := gw.client.Frontends[req.Frontend]
		if !ok {
			return nil, fmt.Errorf("invalid frontend: %s", req.Frontend)
		}

		llbRes, err := f.Solve(
			ctx,
			gw,
			gw.client.Worker, // also implements Executor
			req.FrontendOpt,
			req.FrontendInputs,
			sid,
			gw.client.SessionManager,
		)
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
		return llbRes, nil

	default:
		return &bkfrontend.Result{}, nil
	}
}

type secretTranslatorKey struct{}

type SecretTranslator func(name string, optional bool) (string, error)

func WithSecretTranslator(ctx context.Context, s SecretTranslator) context.Context {
	return context.WithValue(ctx, secretTranslatorKey{}, s)
}

func SecretTranslatorFromContext(ctx context.Context) SecretTranslator {
	v := ctx.Value(secretTranslatorKey{})
	if v == nil {
		return nil
	}
	return v.(SecretTranslator)
}

func ToEntitlementStrings(ents entitlements.Set) []string {
	var out []string
	for ent := range ents {
		out = append(out, string(ent))
	}
	return out
}
