package engineutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"

	contentapi "github.com/containerd/containerd/api/services/content/v1"
	imagesapi "github.com/containerd/containerd/api/services/images/v1"
	leasesapi "github.com/containerd/containerd/api/services/leases/v1"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	contentproxy "github.com/containerd/containerd/v2/core/content/proxy"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	leasesproxy "github.com/containerd/containerd/v2/core/leases/proxy"
	runc "github.com/containerd/go-runc"
	"github.com/dagger/dagger/dagql"
	imageexport "github.com/dagger/dagger/engine/engineutil/imageexport"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	containerdsnapshot "github.com/dagger/dagger/engine/snapshots/containerd"
	snapshot "github.com/dagger/dagger/engine/snapshots/snapshotter"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	bkgw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/internal/buildkit/util/entitlements"
	"github.com/dagger/dagger/internal/buildkit/util/network"
	"github.com/docker/docker/pkg/idtools"
	"github.com/hashicorp/go-multierror"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
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

// Opts combines server-scoped runtime dependencies with per-client session plumbing.
// Server-scoped fields are initialized once with NewOpts and shallow-copied for each client.
type Opts struct {
	ID               string
	Labels           map[string]string
	Platforms        []ocispecs.Platform
	NetworkProviders map[pb.NetMode]network.Provider
	Snapshotter      snapshot.Snapshotter
	ContentStore     *containerdsnapshot.Store
	Applier          diff.Applier
	Differ           diff.Comparer
	IdentityMapping  *idtools.IdentityMapping

	ExecutorRoot    string
	TelemetryPubSub http.Handler
	SessionHandler  sessionHandler

	Runc                *runc.Runc
	DefaultCgroupParent string
	ProcessMode         oci.ProcessMode
	DNSConfig           *oci.DNSConfig
	ApparmorProfile     string
	SELinux             bool
	Entitlements        entitlements.Set

	HostMntNS  *os.File
	CleanMntNS *os.File

	SessionManager      *bksession.Manager
	Dialer              *net.Dialer
	GetClientCaller     func(string) (bksession.Caller, error)
	GetMainClientCaller func() (bksession.Caller, error)
	GetRegistryResolver func(context.Context) (*serverresolver.Resolver, error)

	Interactive        bool
	InteractiveCommand []string

	imageExportWriter *imageexport.Writer
	running           map[string]*execState
	runningMu         *sync.RWMutex
}

// Client is dagger's internal engine utility client
type Client struct {
	*Opts

	closeCtx context.Context
	cancel   context.CancelCauseFunc
	closeMu  sync.RWMutex
}

type sessionHandler interface {
	ServeHTTPToNestedClient(http.ResponseWriter, *http.Request, *engine.ClientMetadata, string, dagql.AnyObjectResult, dagql.Typed, dagql.AnyObjectResult)
}

func NewOpts(opts Opts) (*Opts, error) {
	imageWriter, err := imageexport.NewWriter(imageexport.WriterOpt{
		Snapshotter:  opts.Snapshotter,
		ContentStore: opts.ContentStore,
		Applier:      opts.Applier,
		Differ:       opts.Differ,
	})
	if err != nil {
		return nil, fmt.Errorf("create image writer: %w", err)
	}

	opts.imageExportWriter = imageWriter
	opts.running = make(map[string]*execState)
	opts.runningMu = &sync.RWMutex{}
	return &opts, nil
}

func NewClient(ctx context.Context, opts *Opts) (*Client, error) {
	if opts.imageExportWriter == nil {
		imageWriter, err := imageexport.NewWriter(imageexport.WriterOpt{
			Snapshotter:  opts.Snapshotter,
			ContentStore: opts.ContentStore,
			Applier:      opts.Applier,
			Differ:       opts.Differ,
		})
		if err != nil {
			return nil, fmt.Errorf("create image writer: %w", err)
		}
		opts.imageExportWriter = imageWriter
	}
	if opts.running == nil {
		opts.running = make(map[string]*execState)
	}
	if opts.runningMu == nil {
		opts.runningMu = &sync.RWMutex{}
	}

	// override the outer cancel, we will manage cancellation ourselves here
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	client := &Client{
		Opts:     opts,
		closeCtx: ctx,
		cancel:   cancel,
	}

	return client, nil
}

func (opts *Opts) Close() error {
	if opts == nil {
		return nil
	}
	var rerr error
	for _, provider := range opts.NetworkProviders {
		if err := provider.Close(); err != nil {
			rerr = multierror.Append(rerr, err)
		}
	}
	return rerr
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

func (c *Client) NewNetworkNamespace(ctx context.Context, hostname string) (Namespaced, error) {
	return c.newNetNS(ctx, hostname)
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

	c.runningMu.RLock()
	runState, ok := c.running[ns.NamespaceID()]
	c.runningMu.RUnlock()
	if !ok {
		return zero, fmt.Errorf("namespace for %s not found in running state", ns.NamespaceID())
	}

	return runInNetNS(ctx, runState, fn)
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
		// Git config is best-effort: it provides url.*.insteadOf mappings
		// but is not required for normal operation. Ignore errors from
		// missing git or failed config retrieval (e.g. broken .git
		// pointers inside containers with mounted worktrees).
		if result.Error.Type == git.NOT_FOUND || result.Error.Type == git.CONFIG_RETRIEVAL_FAILED {
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
	return ctx
}
