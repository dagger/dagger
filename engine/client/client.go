package client

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Khan/genqlient/graphql"
	controlapi "github.com/dagger/dagger/internal/buildkit/api/services/control"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/identity"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	bkauth "github.com/dagger/dagger/internal/buildkit/session/auth"
	"github.com/dagger/dagger/internal/buildkit/session/auth/authprovider"
	"github.com/dagger/dagger/internal/buildkit/session/filesync"
	"github.com/dagger/dagger/internal/buildkit/session/secrets"
	"github.com/dagger/dagger/internal/buildkit/session/sshforward"
	"github.com/dagger/dagger/internal/buildkit/util/grpcerrors"
	"github.com/docker/cli/cli/config"
	"github.com/google/uuid"
	"github.com/vito/go-sse/sse"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"golang.org/x/net/http2"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/encoding/protojson"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/drivers"
	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/engine/client/secretprovider"
	"github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/engine/session/h2c"
	"github.com/dagger/dagger/engine/session/pipe"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/dagger/dagger/engine/session/store"
	"github.com/dagger/dagger/engine/session/terminal"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/internal/cloud/auth"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

const (
	// cache configs that should be applied to be import and export
	cacheConfigEnvName = "_EXPERIMENTAL_DAGGER_CACHE_CONFIG"
	// cache configs for imports only
	cacheImportsConfigEnvName = "_EXPERIMENTAL_DAGGER_CACHE_IMPORT_CONFIG"
	// cache configs for exports only
	cacheExportsConfigEnvName = "_EXPERIMENTAL_DAGGER_CACHE_EXPORT_CONFIG"
	// allow enabling scale-out of checks to support cloud
	enableChecksScaleOutEnvName = "_EXPERIMENTAL_DAGGER_CHECKS_SCALE_OUT"
	// shutdown timeout, default is 10s
	shutdownTimeoutEnvName = "_EXPERIMENTAL_DAGGER_SHUTDOWN_TIMEOUT"
)

type Params struct {
	// The id to connect to the API server with. If blank, will be set to a
	// new random value.
	ID string

	// The id of the session to connect to, or if blank a new one should be started.
	SessionID string

	Version string

	SecretToken string

	RunnerHost string // host of dagger engine runner serving buildkit apis

	DisableHostRW bool

	CloudURLCallback func(context.Context, string, string, bool)

	EngineTrace   sdktrace.SpanExporter
	EngineLogs    sdklog.Exporter
	EngineMetrics []sdkmetric.Exporter

	// Log level (0 = INFO)
	LogLevel slog.Level

	Interactive        bool
	InteractiveCommand []string

	WithTerminal terminal.WithTerminalFunc

	AllowedLLMModules []string

	PromptHandler prompt.PromptHandler

	Stdin  io.Reader
	Stdout io.Writer

	ImageLoaderBackend imageload.Backend

	Module   string
	Function string
	ExecCmd  []string

	EagerRuntime bool

	CloudAuth           *auth.Cloud
	EnableCloudScaleOut bool
}

type Client struct {
	Params

	eg *errgroup.Group

	connector   drivers.Connector
	imageLoader *imageload.Loader

	internalCtx    context.Context
	internalCancel context.CancelCauseFunc

	closeCtx      context.Context
	closeRequests context.CancelCauseFunc
	closeMu       sync.RWMutex

	telemetry *errgroup.Group

	httpClient *httpClient
	bkClient   *bkclient.Client
	bkVersion  string
	bkName     string
	numCPU     int
	sessionSrv *BuildkitSessionServer

	// A client for the dagger API that is directly hooked up to this engine client.
	// Currently used for the dagger CLI so it can avoid making a subprocess of itself...
	daggerClient *dagger.Client

	upstreamCacheImportOptions []*controlapi.CacheOptionsEntry
	upstreamCacheExportOptions []*controlapi.CacheOptionsEntry

	hostname       string
	stableClientID string

	nestedSessionPort int

	labels enginetel.Labels

	isCloudScaleOutClient bool
}

func Connect(ctx context.Context, params Params) (_ *Client, rerr error) {
	c := &Client{Params: params}

	if c.ID == "" {
		c.ID = os.Getenv("DAGGER_SESSION_CLIENT_ID")
	}
	if c.ID == "" {
		c.ID = identity.NewID()
	}
	configuredSessionID := c.SessionID
	if c.SessionID == "" {
		c.SessionID = identity.NewID()
	}
	if c.SecretToken == "" {
		c.SecretToken = uuid.New().String()
	}

	c.EnableCloudScaleOut = c.EnableCloudScaleOut || os.Getenv(enableChecksScaleOutEnvName) != ""

	// NB: decouple from the originator's cancel ctx
	c.internalCtx, c.internalCancel = context.WithCancelCause(context.WithoutCancel(ctx))
	c.closeCtx, c.closeRequests = context.WithCancelCause(context.WithoutCancel(ctx))

	c.eg, c.internalCtx = errgroup.WithContext(c.internalCtx)

	defer func() {
		if rerr != nil {
			c.internalCancel(errors.New("Connect failed"))
		}
	}()

	workdir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get workdir: %w", err)
	}

	c.labels = enginetel.LoadDefaultLabels(workdir, engine.Version)

	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("get hostname: %w", err)
	}
	c.hostname = hostname

	connectSpanOpts := []trace.SpanStartOption{}
	if configuredSessionID != "" {
		// infer that this is not a main client caller, server ID is never set for those currently
		connectSpanOpts = append(connectSpanOpts, telemetry.Internal())
	}

	// NB: don't propagate this ctx, we don't want everything tucked beneath connect
	connectCtx, span := Tracer(ctx).Start(ctx, "connect", connectSpanOpts...)
	defer telemetry.EndWithCause(span, &rerr)
	slog := slog.SpanLogger(connectCtx, InstrumentationLibrary)

	nestedSessionPortVal, isNestedSession := os.LookupEnv("DAGGER_SESSION_PORT")
	if isNestedSession {
		nestedSessionPort, err := strconv.Atoi(nestedSessionPortVal)
		if err != nil {
			return nil, fmt.Errorf("parse DAGGER_SESSION_PORT: %w", err)
		}
		c.nestedSessionPort = nestedSessionPort
		c.SecretToken = os.Getenv("DAGGER_SESSION_TOKEN")
		numCPUVal := os.Getenv("DAGGER_ENGINE_NUM_CPU")
		if numCPUVal != "" {
			numCPU, err := strconv.Atoi(numCPUVal)
			if err != nil {
				return nil, fmt.Errorf("parse DAGGER_ENGINE_NUM_CPU: %w", err)
			}
			c.numCPU = numCPU
		}
		c.httpClient = c.newHTTPClient()
		if err := c.init(connectCtx); err != nil {
			return nil, fmt.Errorf("initialize nested client: %w", err)
		}
		if err := c.subscribeTelemetry(connectCtx); err != nil {
			return nil, fmt.Errorf("subscribe to telemetry: %w", err)
		}
		if err := c.daggerConnect(connectCtx); err != nil {
			return nil, fmt.Errorf("failed to connect to dagger: %w", err)
		}
		return c, nil
	}

	// Check if any of the upstream cache importers/exporters are enabled.
	// Note that this is not the cache service support in engine/cache/, that
	// is a different feature which is configured in the engine daemon.
	c.upstreamCacheImportOptions, c.upstreamCacheExportOptions, err = allCacheConfigsFromEnv()
	if err != nil {
		return nil, fmt.Errorf("cache config from env: %w", err)
	}

	c.stableClientID = GetHostStableID(slog)

	if err := c.startEngine(connectCtx, params); err != nil {
		return nil, fmt.Errorf("start engine: %w", err)
	}
	if !engine.CheckVersionCompatibility(engine.NormalizeVersion(c.bkVersion), engine.MinimumEngineVersion) {
		return nil, fmt.Errorf("incompatible engine version %s", engine.NormalizeVersion(c.bkVersion))
	}

	defer func() {
		if rerr != nil {
			c.bkClient.Close()
		}
	}()

	if err := c.startSession(connectCtx); err != nil {
		return nil, fmt.Errorf("start session: %w", err)
	}

	defer func() {
		if rerr != nil {
			c.sessionSrv.Stop()
		}
	}()

	if err := c.subscribeTelemetry(connectCtx); err != nil {
		return nil, fmt.Errorf("subscribe to telemetry: %w", err)
	}

	if err := c.daggerConnect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to dagger: %w", err)
	}

	return c, nil
}

type EngineToEngineParams struct {
	Params

	// The caller's session grpc conn, which will be proxied back to
	CallerSessionConn *grpc.ClientConn

	// important we forward the original client's stable id so that filesync
	// caching can work as expected
	StableClientID string

	Labels enginetel.Labels
}

// ConnectEngineToEngine connects a Dagger client to another Dagger engine using an existing session connection.
// Session attachables are proxied back to the original client.
func ConnectEngineToEngine(ctx context.Context, params EngineToEngineParams) (_ *Client, rerr error) {
	c := &Client{
		Params:                params.Params,
		isCloudScaleOutClient: true,
	}

	if c.ID == "" {
		c.ID = os.Getenv("DAGGER_SESSION_CLIENT_ID")
	}
	if c.ID == "" {
		c.ID = identity.NewID()
	}
	if c.SessionID == "" {
		c.SessionID = identity.NewID()
	}
	if c.SecretToken == "" {
		c.SecretToken = uuid.New().String()
	}

	// NB: decouple from the originator's cancel ctx
	c.internalCtx, c.internalCancel = context.WithCancelCause(context.WithoutCancel(ctx))
	c.closeCtx, c.closeRequests = context.WithCancelCause(context.WithoutCancel(ctx))

	c.eg, c.internalCtx = errgroup.WithContext(c.internalCtx)

	defer func() {
		if rerr != nil {
			c.internalCancel(errors.New("Connect failed"))
		}
	}()

	c.labels = params.Labels

	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("get hostname: %w", err)
	}
	c.hostname = hostname

	c.stableClientID = params.StableClientID

	// NB: don't propagate this ctx, we don't want everything tucked beneath connect
	connectCtx, span := Tracer(ctx).Start(ctx, "connect to cloud engine")
	defer telemetry.EndWithCause(span, &rerr)

	if err := c.startEngine(connectCtx, params.Params); err != nil {
		return nil, fmt.Errorf("start engine: %w", err)
	}
	if !engine.CheckVersionCompatibility(engine.NormalizeVersion(c.bkVersion), engine.MinimumEngineVersion) {
		return nil, fmt.Errorf("incompatible engine version %s", engine.NormalizeVersion(c.bkVersion))
	}

	defer func() {
		if rerr != nil {
			c.bkClient.Close()
		}
	}()

	if err := c.startE2ESession(connectCtx, params.CallerSessionConn); err != nil {
		return nil, fmt.Errorf("start session: %w", err)
	}

	defer func() {
		if rerr != nil {
			c.sessionSrv.Stop()
		}
	}()

	if err := c.subscribeTelemetry(connectCtx); err != nil {
		return nil, fmt.Errorf("subscribe to telemetry: %w", err)
	}

	if err := c.daggerConnect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to dagger: %w", err)
	}

	return c, nil
}

func (c *Client) startEngine(ctx context.Context, params Params) (rerr error) {
	remote, err := url.Parse(c.RunnerHost)
	if err != nil {
		return fmt.Errorf("parse runner host: %w", err)
	}

	driver, err := drivers.GetDriver(ctx, remote.Scheme)
	if err != nil {
		return err
	}

	var cloudToken string
	if v, ok := os.LookupEnv(drivers.EnvDaggerCloudToken); ok {
		cloudToken = v
	} else if _, ok := os.LookupEnv(envDaggerCloudCachetoken); ok {
		cloudToken = v
	}

	if c.CloudURLCallback != nil {
		if url, msg, ok := enginetel.URLForTrace(ctx); ok {
			c.CloudURLCallback(ctx, url, msg, ok)
		} else {
			c.CloudURLCallback(ctx, "https://dagger.cloud/traces/setup", "", ok)
		}
	}

	provisionCtx, provisionSpan := Tracer(ctx).Start(ctx, "starting engine")
	provisionCtx, provisionCancel := context.WithTimeout(provisionCtx, 10*time.Minute)
	c.connector, err = driver.Provision(provisionCtx, remote, &drivers.DriverOpts{
		DaggerCloudToken: cloudToken,
		GPUSupport:       os.Getenv(drivers.EnvGPUSupport),
		Module:           params.Module,
		Function:         params.Function,
		ExecCmd:          params.ExecCmd,
		ClientID:         c.ID,
		CloudAuth:        params.CloudAuth,
	})
	provisionCancel()
	telemetry.EndWithCause(provisionSpan, &err)
	if err != nil {
		return err
	}

	ctx, span := Tracer(ctx).Start(ctx, "connecting to engine", telemetry.Encapsulate())
	defer telemetry.EndWithCause(span, &rerr)

	slog := slog.SpanLogger(ctx, InstrumentationLibrary)
	slog.Debug("connecting", "runner", c.RunnerHost)

	bkCtx, span := Tracer(ctx).Start(ctx, "creating client")
	bkClient, bkInfo, err := newBuildkitClient(bkCtx, remote, c.connector)
	telemetry.EndWithCause(span, &err)
	if err != nil {
		return fmt.Errorf("new client: %w", err)
	}
	c.bkClient = bkClient
	c.bkVersion = bkInfo.BuildkitVersion.Version
	c.bkName = bkInfo.BuildkitVersion.Revision
	c.numCPU = bkInfo.SystemInfo.NumCPU

	slog.Info("connected", "name", c.bkName, "client-version", engine.Version, "server-version", c.bkVersion)

	imageBackend := c.ImageLoaderBackend
	if imageBackend == nil {
		imageBackend = driver.ImageLoader(ctx)
	}
	if imageBackend != nil {
		imgloadCtx, span := Tracer(ctx).Start(ctx, "configuring image store")
		c.imageLoader, err = imageBackend.Loader(imgloadCtx)
		if err != nil {
			err = fmt.Errorf("failed to get image loader: %w", err)
		}
		telemetry.EndWithCause(span, &err)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) subscribeTelemetry(ctx context.Context) (rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "subscribing to telemetry",
		telemetry.Encapsulated())
	defer telemetry.EndWithCause(span, &rerr)

	slog := slog.With("client", c.ID)

	slog.Debug("subscribing to telemetry", "remote", c.RunnerHost)

	c.telemetry = new(errgroup.Group)
	httpClient := c.newTelemetryHTTPClient()
	if c.EngineTrace != nil {
		if err := c.exportTraces(ctx, httpClient); err != nil {
			return fmt.Errorf("export traces: %w", err)
		}
	}
	if c.EngineLogs != nil {
		if err := c.exportLogs(ctx, httpClient); err != nil {
			return fmt.Errorf("export logs: %w", err)
		}
	}
	if c.EngineMetrics != nil {
		if err := c.exportMetrics(ctx, httpClient); err != nil {
			return fmt.Errorf("export metrics: %w", err)
		}
	}
	return nil
}

func (c *Client) startSession(ctx context.Context) (rerr error) {
	ctx, sessionSpan := Tracer(ctx).Start(ctx, "starting session", telemetry.Encapsulate())
	defer telemetry.EndWithCause(sessionSpan, &rerr)

	clientMetadata := c.clientMetadata()
	c.internalCtx = engine.ContextWithClientMetadata(c.internalCtx, &clientMetadata)

	attachables := []bksession.Attachable{
		// sockets
		SocketProvider{EnableHostNetworkAccess: !c.DisableHostRW},
		// secrets
		secretprovider.NewSecretProvider(),
		// registry auth
		authprovider.NewDockerAuthProvider(config.LoadDefaultConfigFile(os.Stderr), nil),
		// host=>container networking
		h2c.NewTunnelListenerAttachable(ctx),
		// terminal
		terminal.NewTerminalAttachable(ctx, c.Params.WithTerminal),
		// Git attachable
		git.NewGitAttachable(ctx),
	}

	if c.Params.Stdin != nil && c.Params.Stdout != nil {
		// pipe
		attachables = append(attachables, pipe.NewPipeAttachable(ctx, c.Params.Stdin, c.Params.Stdout))
	}

	// filesync
	if !c.DisableHostRW {
		filesyncer, err := NewFilesyncer()
		if err != nil {
			return fmt.Errorf("new filesyncer: %w", err)
		}
		attachables = append(attachables, filesyncer.AsSource(), filesyncer.AsTarget())
	}
	if c.Params.PromptHandler != nil {
		attachables = append(attachables, prompt.NewPromptAttachable(c.Params.PromptHandler))
	}

	if c.imageLoader != nil {
		attachable, err := store.NewImageLoaderAttachable(c.imageLoader)
		if err != nil {
			return err
		}
		attachables = append(attachables, attachable)
	}

	sessionConn, err := c.DialContext(ctx, "", "")
	if err != nil {
		return fmt.Errorf("dial for session attachables: %w", err)
	}
	defer func() {
		if rerr != nil {
			sessionConn.Close()
		}
	}()

	c.sessionSrv, err = ConnectBuildkitSession(ctx,
		sessionConn,
		c.AppendHTTPRequestHeaders(http.Header{}),
		attachables...,
	)
	if err != nil {
		return fmt.Errorf("connect buildkit session: %w", err)
	}

	c.eg.Go(func() error {
		ctx, cancel, err := c.withClientCloseCancel(ctx)
		if err != nil {
			return err
		}
		go func() {
			<-ctx.Done()
			cancel(errors.New("startSession context done"))
		}()
		c.sessionSrv.Run(ctx)
		return nil
	})

	c.httpClient = c.newHTTPClient()
	return nil
}

func (c *Client) startE2ESession(ctx context.Context, callerSessionConn *grpc.ClientConn) (rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "starting scale-out session",
		telemetry.Encapsulated())
	defer telemetry.EndWithCause(span, &rerr)

	clientMetadata := c.clientMetadata()
	c.internalCtx = engine.ContextWithClientMetadata(c.internalCtx, &clientMetadata)

	// session attachables that proxy back to the original caller's session
	attachables := []bksession.Attachable{
		FilesyncSourceProxy{
			Client: filesync.NewFileSyncClient(callerSessionConn),
		},
		FilesyncTargetProxy{
			Client: filesync.NewFileSendClient(callerSessionConn),
		},
		secretprovider.NewSecretProviderProxy(secrets.NewSecretsClient(callerSessionConn)),
		NewSocketSessionProxy(sshforward.NewSSHClient(callerSessionConn)),
		NewAuthProxy(bkauth.NewAuthClient(callerSessionConn)),
		h2c.NewTunnelListenerProxy(h2c.NewTunnelListenerClient(callerSessionConn)),
		terminal.NewTerminalProxy(terminal.NewTerminalClient(callerSessionConn)),
		git.NewGitAttachableProxy(git.NewGitClient(callerSessionConn)),
		pipe.NewPipeProxy(pipe.NewPipeClient(callerSessionConn)),
		prompt.NewPromptProxy(prompt.NewPromptClient(callerSessionConn)),
		store.NewStoreProxy(callerSessionConn),
	}

	sessionConn, err := c.DialContext(ctx, "", "")
	if err != nil {
		return fmt.Errorf("dial for session attachables: %w", err)
	}
	defer func() {
		if rerr != nil {
			sessionConn.Close()
		}
	}()

	c.sessionSrv, err = ConnectBuildkitSession(ctx,
		sessionConn,
		c.AppendHTTPRequestHeaders(http.Header{}),
		attachables...,
	)
	if err != nil {
		return fmt.Errorf("connect buildkit session: %w", err)
	}

	c.eg.Go(func() error {
		ctx, cancel, err := c.withClientCloseCancel(ctx)
		if err != nil {
			return err
		}
		go func() {
			<-ctx.Done()
			cancel(errors.New("startSession context done"))
		}()
		c.sessionSrv.Run(ctx)
		return nil
	})

	c.httpClient = c.newHTTPClient()
	return nil
}

func ConnectBuildkitSession(
	ctx context.Context,
	conn net.Conn,
	headers http.Header,
	attachables ...bksession.Attachable,
) (*BuildkitSessionServer, error) {
	sessionSrv := NewBuildkitSessionServer(ctx, conn, attachables...)
	for _, methodURL := range sessionSrv.MethodURLs {
		headers.Add(engine.SessionMethodNameMetaKey, methodURL)
	}
	telemetry.Propagator.Inject(ctx, propagation.HeaderCarrier(headers))

	req := &http.Request{
		Method: http.MethodGet,
		URL: &url.URL{
			Scheme: "http",
			Host:   "dagger",
			Path:   engine.SessionAttachablesEndpoint,
		},
		Header: headers,
		Host:   "dagger",
	}
	if err := req.Write(conn); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		var respBody []byte
		if resp.Body != nil {
			respBody, _ = io.ReadAll(resp.Body)
		}
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	// We tell the server that we have fully read the response and will now switch to serving gRPC
	// by sending a single byte ack. This prevents the server from starting to send gRPC client
	// traffic while we are still reading the previous HTTP response.
	if _, err := conn.Write([]byte{0}); err != nil {
		return nil, fmt.Errorf("write ack: %w", err)
	}

	return sessionSrv, nil
}

func NewBuildkitSessionServer(ctx context.Context, conn net.Conn, attachables ...bksession.Attachable) *BuildkitSessionServer {
	sessionSrvOpts := []grpc.ServerOption{
		grpc.UnaryInterceptor(grpcerrors.UnaryServerInterceptor),
		grpc.StreamInterceptor(grpcerrors.StreamServerInterceptor),
	}

	srv := grpc.NewServer(sessionSrvOpts...)
	grpc_health_v1.RegisterHealthServer(srv, health.NewServer())
	for _, attachable := range attachables {
		attachable.Register(srv)
	}

	var methodURLs []string
	for name, svc := range srv.GetServiceInfo() {
		for _, method := range svc.Methods {
			methodURLs = append(methodURLs, bksession.MethodURL(name, method.Name))
		}
	}

	return &BuildkitSessionServer{
		Server:      srv,
		Attachables: attachables,
		MethodURLs:  methodURLs,
		Conn:        conn,
	}
}

type BuildkitSessionServer struct {
	*grpc.Server
	MethodURLs  []string
	Conn        net.Conn
	Attachables []bksession.Attachable
}

func (srv *BuildkitSessionServer) Run(ctx context.Context) {
	defer srv.Conn.Close()
	defer srv.Stop()

	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		(&http2.Server{}).ServeConn(srv.Conn, &http2.ServeConnOpts{
			Context: ctx,
			Handler: srv.Server,
		})
	}()

	select {
	case <-ctx.Done():
	case <-doneCh:
	}
}

func (c *Client) daggerConnect(ctx context.Context) error {
	var err error
	c.daggerClient, err = dagger.Connect(
		context.WithoutCancel(ctx),
		dagger.WithConn(EngineConn(c)),
	)
	return err
}

func (c *Client) Close() (rerr error) {
	// shutdown happens outside of c.closeMu, since it requires a connection
	if err := c.shutdownServer(); err != nil {
		rerr = errors.Join(rerr, fmt.Errorf("shutdown: %w", err))
	}

	c.closeMu.Lock()
	defer c.closeMu.Unlock()

	select {
	case <-c.closeCtx.Done():
		// already closed
		return nil
	default:
	}

	c.closeRequests(errors.New("Client.Close"))

	if c.internalCancel != nil {
		c.internalCancel(errors.New("Client.Close"))
	}

	if c.daggerClient != nil {
		c.eg.Go(c.daggerClient.Close)
	}

	if c.httpClient != nil {
		c.eg.Go(c.httpClient.Close)
	}

	if c.sessionSrv != nil {
		c.eg.Go(func() error {
			c.sessionSrv.Stop()
			return nil
		})
		for _, attachable := range c.sessionSrv.Attachables {
			closeable, ok := attachable.(io.Closer)
			if !ok {
				continue
			}
			c.eg.Go(func() error {
				return closeable.Close()
			})
		}
	}
	if c.bkClient != nil {
		c.eg.Go(c.bkClient.Close)
	}
	if err := c.eg.Wait(); err != nil {
		rerr = errors.Join(rerr, err)
	}

	// Wait for telemetry to finish draining
	if c.telemetry != nil {
		if err := c.telemetry.Wait(); err != nil {
			rerr = errors.Join(rerr, fmt.Errorf("wait for telemetry: %w", err))
		}
	}

	return rerr
}

type otlpConsumer struct {
	httpClient *httpClient
	path       string
	traceID    trace.TraceID
	clientID   string
	eg         *errgroup.Group
}

func (c *otlpConsumer) Consume(ctx context.Context, cb func([]byte) error) (rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "consuming "+c.path)
	defer telemetry.EndWithCause(span, &rerr)

	slog := slog.With("path", c.path, "traceID", c.traceID, "clientID", c.clientID)

	defer func() {
		if rerr != nil {
			slog.Error("consume failed", "err", rerr)
		} else {
			slog.ExtraDebug("done consuming", "ctxErr", ctx.Err())
		}
	}()

	sseConn, err := sse.Connect(c.httpClient, time.Second, func() *http.Request {
		return (&http.Request{
			Method: http.MethodGet,
			URL: &url.URL{
				Scheme: "http",
				Host:   "dagger",
				Path:   c.path,
			},
		}).WithContext(ctx)
	})
	if err != nil {
		return fmt.Errorf("connect to SSE: %w", err)
	}

	c.eg.Go(func() error {
		defer sseConn.Close()

		for {
			event, err := sseConn.Next()
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
					return nil
				}
				return fmt.Errorf("decode: %w", err)
			}
			if event.Name == "attached" {
				continue
			}

			data := event.Data

			span.AddEvent("data", trace.WithAttributes(
				attribute.String("cursor", event.ID),
				attribute.Int("bytes", len(data)),
			))

			if len(data) == 0 {
				continue
			}

			if err := cb(data); err != nil {
				slog.Warn("consume error", "err", err)
				span.AddEvent("consume error", trace.WithAttributes(
					attribute.String("error", err.Error()),
				))
			}
		}
	})

	return nil
}

func (c *Client) exportTraces(ctx context.Context, httpClient *httpClient) error {
	// NB: we never actually want to interrupt this, since it's relied upon for
	// seeing what's going on, even during shutdown
	ctx = context.WithoutCancel(ctx)

	exp := &otlpConsumer{
		path:       "/v1/traces",
		traceID:    trace.SpanContextFromContext(ctx).TraceID(),
		clientID:   c.ID,
		httpClient: httpClient,
		eg:         c.telemetry,
	}

	return exp.Consume(ctx, func(data []byte) error {
		var req coltracepb.ExportTraceServiceRequest
		if err := protojson.Unmarshal(data, &req); err != nil {
			return fmt.Errorf("unmarshal: %w", err)
		}

		spans := telemetry.SpansFromPB(req.GetResourceSpans())

		slog.ExtraDebug("received spans from engine", "len", len(spans))

		for _, span := range spans {
			slog.ExtraDebug("received span from engine", "span", span.Name(), "id", span.SpanContext().SpanID(), "endTime", span.EndTime())
		}

		if err := c.Params.EngineTrace.ExportSpans(ctx, spans); err != nil {
			return fmt.Errorf("export %d spans: %w", len(spans), err)
		}

		return nil
	})
}

func (c *Client) exportLogs(ctx context.Context, httpClient *httpClient) error {
	// NB: we never actually want to interrupt this, since it's relied upon for
	// seeing what's going on, even during shutdown
	ctx = context.WithoutCancel(ctx)

	exp := &otlpConsumer{
		path:       "/v1/logs",
		traceID:    trace.SpanContextFromContext(ctx).TraceID(),
		clientID:   c.ID,
		httpClient: httpClient,
		eg:         c.telemetry,
	}

	return exp.Consume(ctx, func(data []byte) error {
		var req collogspb.ExportLogsServiceRequest
		if err := protojson.Unmarshal(data, &req); err != nil {
			return fmt.Errorf("unmarshal spans: %w", err)
		}
		if err := telemetry.ReexportLogsFromPB(ctx, c.EngineLogs, &req); err != nil {
			return fmt.Errorf("re-export logs: %w", err)
		}
		return nil
	})
}

func (c *Client) exportMetrics(ctx context.Context, httpClient *httpClient) error {
	// NB: we never actually want to interrupt this, since it's relied upon for
	// seeing what's going on, even during shutdown
	ctx = context.WithoutCancel(ctx)

	exp := &otlpConsumer{
		path:       "/v1/metrics",
		traceID:    trace.SpanContextFromContext(ctx).TraceID(),
		clientID:   c.ID,
		httpClient: httpClient,
		eg:         c.telemetry,
	}

	return exp.Consume(ctx, func(data []byte) error {
		var req colmetricspb.ExportMetricsServiceRequest
		if err := protojson.Unmarshal(data, &req); err != nil {
			return fmt.Errorf("unmarshal metrics: %w", err)
		}
		if err := enginetel.ReexportMetricsFromPB(ctx, c.EngineMetrics, &req); err != nil {
			return fmt.Errorf("re-export metrics: %w", err)
		}
		return nil
	})
}

func (c *Client) init(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "http://dagger"+engine.InitEndpoint, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	req.SetBasicAuth(c.SecretToken, "")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do init: %w", err)
	}

	return resp.Body.Close()
}

func (c *Client) shutdownServer() error {
	// don't immediately cancel shutdown if we're shutting down because we were
	// canceled
	ctx := context.WithoutCancel(c.internalCtx)

	timeout := 10 * time.Second
	if timeoutStr, ok := os.LookupEnv(shutdownTimeoutEnvName); ok {
		if interval, err := time.ParseDuration(timeoutStr); err == nil {
			timeout = interval
		} else {
			slog.Warn("invalid "+shutdownTimeoutEnvName+" value, using default 10 seconds", "error", err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "http://dagger"+engine.ShutdownEndpoint, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	req.SetBasicAuth(c.SecretToken, "")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do shutdown: %w", err)
	}

	return resp.Body.Close()
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
			cancel(fmt.Errorf("client closed: %w", context.Cause(c.closeCtx)))
		case <-ctx.Done():
		}
	}()
	return ctx, cancel, nil
}

func (c *Client) DialContext(ctx context.Context, _, _ string) (conn net.Conn, err error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	conn, err = c.dialContextNoClientClose(ctx)
	if err != nil {
		return nil, err
	}
	go func() {
		<-c.closeCtx.Done()
		cancel(errors.New("dial context closed"))
		conn.Close()
	}()
	return conn, nil
}

func (c *Client) dialContextNoClientClose(ctx context.Context) (net.Conn, error) {
	isNestedSession := c.nestedSessionPort != 0
	if isNestedSession {
		return (&net.Dialer{}).DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", c.nestedSessionPort))
	} else {
		return c.connector.Connect(ctx)
	}
}

func (c *Client) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// propagate span from per-request client headers, otherwise all spans
	// end up beneath the client session span
	ctx := telemetry.Propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("client has closed: " + err.Error()))
		return
	}
	r = r.WithContext(ctx)
	defer cancel(errors.New("client done serving HTTP"))

	if c.SecretToken != "" {
		username, _, ok := r.BasicAuth()
		if !ok || username != c.SecretToken {
			w.Header().Set("WWW-Authenticate", `Basic realm="Access to the Dagger engine session"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}

	proxyReq := &http.Request{
		Method: r.Method,
		URL: &url.URL{
			Scheme: "http",
			Host:   "dagger",
			Path:   r.URL.Path,
		},
		Header: r.Header,
		Body:   r.Body,
	}
	proxyReq = proxyReq.WithContext(ctx)

	// handle the case of dagger shell websocket, which hijacks the connection and uses it as a raw net.Conn
	if proxyReq.Header["Upgrade"] != nil && proxyReq.Header["Upgrade"][0] == "websocket" {
		c.serveHijackedHTTP(ctx, cancel, w, proxyReq)
		return
	}

	resp, err := c.httpClient.Do(proxyReq)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("http do: " + err.Error()))
		return
	}
	defer resp.Body.Close()
	maps.Copy(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, err = io.Copy(writeFlusher{w}, resp.Body)
	if err != nil && !errors.Is(err, context.Canceled) {
		panic(err) // don't write header because we already wrote to the body, which isn't allowed
	}
}

// writeFlusher flushes after every write.
//
// This is particularly important for streamed response bodies like /v1/logs and /v1/traces,
// and possibly GraphQL subscriptions in the future, though those might use WebSockets anyway,
// which is already given special treatment.
type writeFlusher struct {
	http.ResponseWriter
}

func (w writeFlusher) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if err != nil {
		return n, err
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
	return n, nil
}

func (c *Client) serveHijackedHTTP(ctx context.Context, cancel context.CancelCauseFunc, w http.ResponseWriter, r *http.Request) {
	slog.Warn("serving hijacked HTTP with trace", "ctx", trace.SpanContextFromContext(ctx).TraceID())

	serverConn, err := c.DialContext(ctx, "", "")
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("dial server: " + err.Error()))
		return
	}
	// DialContext handles closing returned conn when Client is closed

	// Hijack the client conn so we can use it as a raw net.Conn and proxy that w/ the server.
	// Note that after hijacking no more headers can be written, we can only panic (which will
	// get caught by the http server that called ServeHTTP)
	hij, ok := w.(http.Hijacker)
	if !ok {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("not a hijacker"))
		return
	}
	clientConn, _, err := hij.Hijack()
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("hijack: " + err.Error()))
		return
	}
	go func() {
		<-c.closeCtx.Done()
		cancel(errors.New("client closing hijacked HTTP"))
		clientConn.Close()
	}()

	// send the initial client http upgrade request to the server
	r.Header = c.AppendHTTPRequestHeaders(r.Header)
	if err := r.Write(serverConn); err != nil {
		panic(fmt.Errorf("write upgrade request: %w", err))
	}

	// proxy the connections both directions
	var eg errgroup.Group
	eg.Go(func() error {
		defer serverConn.Close()
		defer clientConn.Close()
		_, err := io.Copy(serverConn, clientConn)
		if errors.Is(err, io.EOF) || grpcerrors.Code(err) == codes.Canceled {
			err = nil
		}
		if err != nil {
			return fmt.Errorf("copy client to server: %w", err)
		}
		return nil
	})
	eg.Go(func() error {
		defer serverConn.Close()
		defer clientConn.Close()
		_, err := io.Copy(clientConn, serverConn)
		if errors.Is(err, io.EOF) || grpcerrors.Code(err) == codes.Canceled {
			err = nil
		}
		if err != nil {
			return fmt.Errorf("copy server to client: %w", err)
		}
		return nil
	})
	if err := eg.Wait(); err != nil {
		panic(err)
	}
}

func (c *Client) Do(
	ctx context.Context,
	query string,
	opName string,
	variables map[string]any,
	data any,
) (rerr error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return err
	}
	defer cancel(errors.New("Client.Do done"))

	gqlClient := graphql.NewClient("http://dagger"+engine.QueryEndpoint, c.httpClient)

	req := &graphql.Request{
		Query:     query,
		Variables: variables,
		OpName:    opName,
	}
	resp := &graphql.Response{}

	err = gqlClient.MakeRequest(ctx, req, resp)
	if err != nil {
		return fmt.Errorf("make request: %w", err)
	}
	if resp.Errors != nil {
		errs := make([]error, len(resp.Errors))
		for i, err := range resp.Errors {
			errs[i] = err
		}
		return errors.Join(errs...)
	}

	if data != nil {
		dataBytes, err := json.Marshal(resp.Data)
		if err != nil {
			return fmt.Errorf("marshal data: %w", err)
		}
		err = json.Unmarshal(dataBytes, data)
		if err != nil {
			return fmt.Errorf("unmarshal data: %w", err)
		}
	}
	return nil
}

// A client to the Dagger API hooked up directly with this engine client.
func (c *Client) Dagger() *dagger.Client {
	return c.daggerClient
}

// NumCPU returns the number of CPUs available on the engine host.
func (c *Client) NumCPU() int {
	return c.numCPU
}

// env is in form k1=v1,k2=v2;k3=v3... with ';' used to separate multiple cache configs.
// any value that itself needs ';' can use '\;' to escape it.
func cacheConfigFromEnv(envName string) ([]*controlapi.CacheOptionsEntry, error) {
	envVal, ok := os.LookupEnv(envName)
	if !ok {
		return nil, nil
	}
	configKVs := strings.Split(envVal, ";")
	// handle '\;' as an escape in case ';' needs to be used in a cache config setting rather than as
	// a delimiter between multiple cache configs
	for i := len(configKVs) - 2; i >= 0; i-- {
		if strings.HasSuffix(configKVs[i], `\`) {
			configKVs[i] = configKVs[i][:len(configKVs[i])-1] + ";" + configKVs[i+1]
			configKVs = slices.Delete(configKVs, i+1, i+2)
		}
	}

	cacheConfigs := make([]*controlapi.CacheOptionsEntry, 0, len(configKVs))
	for _, kvsStr := range configKVs {
		kvs := strings.Split(kvsStr, ",")
		if len(kvs) == 0 {
			continue
		}
		attrs := make(map[string]string)
		for _, kv := range kvs {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid form for cache config %q", kv)
			}
			attrs[parts[0]] = parts[1]
		}
		typeVal, ok := attrs["type"]
		if !ok {
			return nil, fmt.Errorf("missing type in cache config: %q", envVal)
		}
		delete(attrs, "type")
		cacheConfigs = append(cacheConfigs, &controlapi.CacheOptionsEntry{
			Type:  typeVal,
			Attrs: attrs,
		})
	}
	return cacheConfigs, nil
}

func allCacheConfigsFromEnv() (cacheImportConfigs []*controlapi.CacheOptionsEntry, cacheExportConfigs []*controlapi.CacheOptionsEntry, rerr error) {
	// cache import only configs
	cacheImportConfigs, err := cacheConfigFromEnv(cacheImportsConfigEnvName)
	if err != nil {
		return nil, nil, fmt.Errorf("cache import config from env: %w", err)
	}

	// cache export only configs
	cacheExportConfigs, err = cacheConfigFromEnv(cacheExportsConfigEnvName)
	if err != nil {
		return nil, nil, fmt.Errorf("cache export config from env: %w", err)
	}

	// this env sets configs for both imports and exports
	cacheConfigs, err := cacheConfigFromEnv(cacheConfigEnvName)
	if err != nil {
		return nil, nil, fmt.Errorf("cache config from env: %w", err)
	}
	for _, cfg := range cacheConfigs {
		cacheImportConfigs = append(cacheImportConfigs, cfg)
		cacheExportConfigs = append(cacheExportConfigs, cfg)
	}

	return cacheImportConfigs, cacheExportConfigs, nil
}

func (c *Client) clientMetadata() engine.ClientMetadata {
	sshAuthSock := os.Getenv("SSH_AUTH_SOCK")
	// expand ~ into absolute path for consistent behavior with CLI
	// ⚠️ When updating clientMetadata's logic, please also update setupNestedClient
	// for consistent behavior of CLI inside nested execution
	homeDir, err := os.UserHomeDir()
	if err == nil {
		expandedPath, err := pathutil.ExpandHomeDir(homeDir, sshAuthSock)
		if err == nil {
			sshAuthSock = expandedPath
		}
	}

	clientVersion := c.Version
	if clientVersion == "" {
		clientVersion = engine.Version
	}

	var cloudOrg string
	if o, _ := auth.CurrentOrgName(); o != "" {
		cloudOrg = o
	}

	var remoteEngineID string
	if c.connector != nil {
		remoteEngineID = c.connector.EngineID()
	}

	return engine.ClientMetadata{
		ClientID:                  c.ID,
		ClientVersion:             clientVersion,
		SessionID:                 c.SessionID,
		ClientSecretToken:         c.SecretToken,
		ClientHostname:            c.hostname,
		ClientStableID:            c.stableClientID,
		UpstreamCacheImportConfig: c.upstreamCacheImportOptions,
		UpstreamCacheExportConfig: c.upstreamCacheExportOptions,
		Labels:                    c.labels.AsMap(),
		CloudOrg:                  cloudOrg,
		DoNotTrack:                analytics.DoNotTrack(),
		Interactive:               c.Interactive,
		InteractiveCommand:        c.InteractiveCommand,
		SSHAuthSocketPath:         sshAuthSock,
		AllowedLLMModules:         c.AllowedLLMModules,
		EagerRuntime:              c.EagerRuntime,
		CloudAuth:                 c.CloudAuth,
		EnableCloudScaleOut:       c.EnableCloudScaleOut,
		CloudScaleOutEngineID:     remoteEngineID,
	}
}

func (c *Client) AppendHTTPRequestHeaders(headers http.Header) http.Header {
	return c.clientMetadata().AppendToHTTPHeaders(headers)
}

func (c *Client) newHTTPClient() *httpClient {
	return &httpClient{
		inner: &http.Client{
			Transport: &http2.Transport{
				AllowHTTP: true,
				DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
					return c.DialContext(ctx, network, addr)
				},
			},
		},
		headers:     c.AppendHTTPRequestHeaders(http.Header{}),
		secretToken: c.SecretToken,
	}
}

func (c *Client) newTelemetryHTTPClient() *httpClient {
	return &httpClient{
		inner: &http.Client{
			Transport: &http2.Transport{
				AllowHTTP: true,
				DialTLSContext: func(ctx context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
					return c.dialContextNoClientClose(ctx)
				},
			},
		},
		headers:     c.AppendHTTPRequestHeaders(http.Header{}),
		secretToken: c.SecretToken,
	}
}

type httpClient struct {
	inner       *http.Client
	headers     http.Header
	secretToken string
}

func (c *httpClient) Do(req *http.Request) (*http.Response, error) {
	for k, v := range c.headers {
		if req.Header == nil {
			req.Header = http.Header{}
		}
		req.Header[k] = v
	}
	telemetry.Propagator.Inject(req.Context(), propagation.HeaderCarrier(req.Header))
	req.SetBasicAuth(c.secretToken, "")

	// We're making a request to the engine HTTP/2 server, but these headers are not
	// allowed in HTTP 2+, so unset them in case they came from an HTTP/1 client that
	// we are proxying a request for.
	req.Header.Del("Connection")
	req.Header.Del("Keep-Alive")

	return c.inner.Do(req)
}

func (c *httpClient) Close() error {
	c.inner.CloseIdleConnections()
	return nil
}

func EngineConn(engineClient *Client) DirectConn {
	return engineClient.httpClient.Do
}

type DirectConn func(*http.Request) (*http.Response, error)

func (f DirectConn) Do(r *http.Request) (*http.Response, error) {
	return f(r)
}

func (f DirectConn) Host() string {
	return "dagger"
}

func (f DirectConn) Close() error {
	return nil
}
