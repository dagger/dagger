package client

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/cenkalti/backoff/v4"
	"github.com/containerd/containerd/defaults"
	"github.com/docker/cli/cli/config"
	"github.com/google/uuid"
	controlapi "github.com/moby/buildkit/api/services/control"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/moby/buildkit/util/grpcerrors"
	"github.com/moby/buildkit/util/tracing"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/net/http2"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/drivers"
	"github.com/dagger/dagger/engine/session"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

type Params struct {
	// The id to connect to the API server with. If blank, will be set to a
	// new random value.
	ID string

	// The id of the session to connect to, or if blank a new one should be started.
	SessionID string

	SecretToken string

	RunnerHost string // host of dagger engine runner serving buildkit apis
	UserAgent  string

	DisableHostRW bool

	EngineCallback func(context.Context, string, string, string)
	CloudCallback  func(context.Context, string, string)

	EngineTrace sdktrace.SpanExporter
	EngineLogs  sdklog.Exporter

	// Log level (0 = INFO)
	LogLevel slog.Level
}

type Client struct {
	Params

	rootCtx context.Context

	eg *errgroup.Group

	connector drivers.Connector

	internalCtx    context.Context
	internalCancel context.CancelFunc

	closeCtx      context.Context
	closeRequests context.CancelFunc
	closeMu       sync.RWMutex

	telemetry     *errgroup.Group
	telemetryConn *grpc.ClientConn

	httpClient *httpClient
	bkClient   *bkclient.Client
	bkVersion  string
	sessionSrv *BuildkitSessionServer

	// A client for the dagger API that is directly hooked up to this engine client.
	// Currently used for the dagger CLI so it can avoid making a subprocess of itself...
	daggerClient *dagger.Client

	upstreamCacheImportOptions []*controlapi.CacheOptionsEntry
	upstreamCacheExportOptions []*controlapi.CacheOptionsEntry

	hostname string

	nestedSessionPort int

	labels enginetel.Labels
}

func Connect(ctx context.Context, params Params) (_ *Client, _ context.Context, rerr error) {
	c := &Client{Params: params}

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

	// keep the root ctx around so we can detect whether we've been interrupted,
	// so we can drain immediately in that scenario
	c.rootCtx = ctx

	// NB: decouple from the originator's cancel ctx
	c.internalCtx, c.internalCancel = context.WithCancel(context.WithoutCancel(ctx))
	c.closeCtx, c.closeRequests = context.WithCancel(context.WithoutCancel(ctx))

	c.eg, c.internalCtx = errgroup.WithContext(c.internalCtx)

	defer func() {
		if rerr != nil {
			c.internalCancel()
		}
	}()

	workdir, err := os.Getwd()
	if err != nil {
		return nil, nil, fmt.Errorf("get workdir: %w", err)
	}

	c.labels = enginetel.LoadDefaultLabels(workdir, engine.Version)

	hostname, err := os.Hostname()
	if err != nil {
		return nil, nil, fmt.Errorf("get hostname: %w", err)
	}
	c.hostname = hostname

	nestedSessionPortVal, isNestedSession := os.LookupEnv("DAGGER_SESSION_PORT")
	if isNestedSession {
		nestedSessionPort, err := strconv.Atoi(nestedSessionPortVal)
		if err != nil {
			return nil, nil, fmt.Errorf("parse DAGGER_SESSION_PORT: %w", err)
		}
		c.nestedSessionPort = nestedSessionPort
		c.SecretToken = os.Getenv("DAGGER_SESSION_TOKEN")
		c.httpClient = c.newHTTPClient()
		if err := c.daggerConnect(ctx); err != nil {
			return nil, nil, fmt.Errorf("failed to connect to dagger: %w", err)
		}
		return c, ctx, nil
	}

	// Check if any of the upstream cache importers/exporters are enabled.
	// Note that this is not the cache service support in engine/cache/, that
	// is a different feature which is configured in the engine daemon.
	c.upstreamCacheImportOptions, c.upstreamCacheExportOptions, err = allCacheConfigsFromEnv()
	if err != nil {
		return nil, nil, fmt.Errorf("cache config from env: %w", err)
	}

	connectSpanOpts := []trace.SpanStartOption{}
	if configuredSessionID != "" {
		// infer that this is not a main client caller, server ID is never set for those currently
		connectSpanOpts = append(connectSpanOpts, telemetry.Internal())
	}

	// NB: don't propagate this ctx, we don't want everything tucked beneath connect
	connectCtx, span := Tracer().Start(ctx, "connect", connectSpanOpts...)
	defer telemetry.End(span, func() error { return rerr })

	if err := c.startEngine(connectCtx); err != nil {
		return nil, nil, fmt.Errorf("start engine: %w", err)
	}
	err = engine.CheckVersionCompatibility(c.bkVersion, engine.MinimumEngineVersion)
	if err != nil {
		return nil, nil, fmt.Errorf("incompatible engine version: %w", err)
	}

	defer func() {
		if rerr != nil {
			c.bkClient.Close()
		}
	}()

	if err := c.startSession(connectCtx); err != nil {
		return nil, nil, fmt.Errorf("start session: %w", err)
	}

	defer func() {
		if rerr != nil {
			c.sessionSrv.Stop()
		}
	}()

	if err := c.daggerConnect(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to connect to dagger: %w", err)
	}

	return c, ctx, nil
}

func (c *Client) startEngine(ctx context.Context) (rerr error) {
	remote, err := url.Parse(c.RunnerHost)
	if err != nil {
		return fmt.Errorf("parse runner host: %w", err)
	}

	driver, err := drivers.GetDriver(remote.Scheme)
	if err != nil {
		return err
	}

	var cloudToken string
	if v, ok := os.LookupEnv(drivers.EnvDaggerCloudToken); ok {
		cloudToken = v
	} else if _, ok := os.LookupEnv(envDaggerCloudCachetoken); ok {
		cloudToken = v
	}

	provisionCtx, provisionSpan := Tracer().Start(ctx, "starting engine")
	provisionCtx, provisionCancel := context.WithTimeout(provisionCtx, 10*time.Minute)
	c.connector, err = driver.Provision(provisionCtx, remote, &drivers.DriverOpts{
		UserAgent:        c.UserAgent,
		DaggerCloudToken: cloudToken,
		GPUSupport:       os.Getenv(drivers.EnvGPUSupport),
	})
	provisionCancel()
	telemetry.End(provisionSpan, func() error { return err })
	if err != nil {
		return err
	}

	ctx, span := Tracer().Start(ctx, "connecting to engine")
	defer telemetry.End(span, func() error { return rerr })

	slog := slog.SpanLogger(ctx, InstrumentationLibrary)

	slog.Debug("connecting", "runner", c.RunnerHost, "client", c.ID)

	bkClient, bkInfo, err := newBuildkitClient(ctx, remote, c.connector)
	if err != nil {
		return fmt.Errorf("new client: %w", err)
	}
	c.bkClient = bkClient
	c.bkVersion = bkInfo.BuildkitVersion.Version

	if err := retry(ctx, 10*time.Millisecond, func(elapsed time.Duration, ctx context.Context) error {
		slog.Debug("subscribing to telemetry", "remote", c.RunnerHost)

		// Open a separate connection for telemetry.
		telemetryConn, err := grpc.NewClient(
			"passthrough:"+c.RunnerHost,
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
				return c.connector.Connect(c.internalCtx)
			}),
			// Propagate the session ID to the server (via baggage).
			grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
			// Same defaults as Buildkit. I hit the default 4MB limit pretty quickly.
			// Shrinking IDs might help.
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(defaults.DefaultMaxRecvMsgSize)),
			grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(defaults.DefaultMaxSendMsgSize)),
			// Uncomment to measure telemetry traffic.
			// grpc.WithUnaryInterceptor(telemetry.MeasuringUnaryClientInterceptor()),
			// grpc.WithStreamInterceptor(telemetry.MeasuringStreamClientInterceptor()),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			slog.Error("failed to dial telemetry", "error", err, "elapsed", elapsed)
			return fmt.Errorf("telemetry grpc dial: %w", err)
		}
		c.telemetryConn = telemetryConn
		c.telemetry = new(errgroup.Group)

		if c.EngineTrace != nil {
			if err := c.exportTraces(enginetel.NewTracesSourceClient(telemetryConn)); err != nil {
				slog.Error("failed to subscribe to traces", "error", err, "elapsed", elapsed)
				return fmt.Errorf("export traces: %w", err)
			}
		}

		if c.EngineLogs != nil {
			if err := c.exportLogs(enginetel.NewLogsSourceClient(telemetryConn)); err != nil {
				slog.Error("failed to subscribe to logs", "error", err, "elapsed", elapsed)
				return fmt.Errorf("export logs: %w", err)
			}
		}

		slog.Debug("subscribed to telemetry", "elapsed", elapsed)

		return nil
	}); err != nil {
		return fmt.Errorf("attach to telemetry: %w", err)
	}

	if c.EngineCallback != nil {
		c.EngineCallback(ctx, bkInfo.BuildkitVersion.Revision, bkInfo.BuildkitVersion.Version, c.ID)
	}
	if c.CloudCallback != nil {
		if url, msg, ok := enginetel.URLForTrace(ctx); ok {
			c.CloudCallback(ctx, url, msg)
		}
	}

	return nil
}

func (c *Client) startSession(ctx context.Context) (rerr error) {
	ctx, sessionSpan := Tracer().Start(ctx, "starting session")
	defer telemetry.End(sessionSpan, func() error { return rerr })

	clientMetadata := c.clientMetadata()
	c.internalCtx = engine.ContextWithClientMetadata(c.internalCtx, &clientMetadata)

	attachables := []bksession.Attachable{
		// sockets
		SocketProvider{EnableHostNetworkAccess: !c.DisableHostRW},
		// registry auth
		authprovider.NewDockerAuthProvider(config.LoadDefaultConfigFile(os.Stderr), nil),
		// host=>container networking
		session.NewTunnelListenerAttachable(ctx, nil),
	}
	// filesync
	if !c.DisableHostRW {
		filesyncer, err := NewFilesyncer("", "", nil, nil)
		if err != nil {
			return fmt.Errorf("new filesyncer: %w", err)
		}
		attachables = append(attachables, filesyncer.AsSource(), filesyncer.AsTarget())
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
			cancel()
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
	if span := trace.SpanFromContext(ctx); span.SpanContext().IsValid() {
		statsHandler := tracing.ServerStatsHandler(
			otelgrpc.WithTracerProvider(span.TracerProvider()),
			otelgrpc.WithPropagators(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})),
		)
		sessionSrvOpts = append(sessionSrvOpts, grpc.StatsHandler(statsHandler))
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
		Server:     srv,
		MethodURLs: methodURLs,
		Conn:       conn,
	}
}

type BuildkitSessionServer struct {
	*grpc.Server
	MethodURLs []string
	Conn       net.Conn
}

func (srv *BuildkitSessionServer) Run(ctx context.Context) {
	defer srv.Conn.Close()

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

func retry(ctx context.Context, initialInterval time.Duration, fn func(time.Duration, context.Context) error) error {
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = initialInterval

	connectRetryCtx, connectRetryCancel := context.WithTimeout(ctx, 300*time.Second)
	defer connectRetryCancel()

	start := time.Now()

	var lastErr error
	err := backoff.Retry(func() error {
		if err := ctx.Err(); err != nil {
			return backoff.Permanent(err)
		}

		nextBackoff := bo.NextBackOff()
		ctx, cancel := context.WithTimeout(connectRetryCtx, nextBackoff)
		defer cancel()

		lastErr = fn(time.Since(start), ctx)
		return lastErr
	}, backoff.WithContext(bo, connectRetryCtx))
	if err != nil {
		return errors.Join(ctx.Err(), lastErr)
	}
	return nil
}

func (c *Client) daggerConnect(ctx context.Context) error {
	var err error
	c.daggerClient, err = dagger.Connect(
		context.WithoutCancel(ctx),
		dagger.WithConn(EngineConn(c)),
		dagger.WithSkipCompatibilityCheck())
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

	c.closeRequests()

	if c.internalCancel != nil {
		c.internalCancel()
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
			rerr = errors.Join(rerr, fmt.Errorf("flush telemetry: %w", err))
		}
	}

	return rerr
}

func (c *Client) exportTraces(tracesClient enginetel.TracesSourceClient) error {
	// NB: we never actually want to interrupt this, since it's relied upon for
	// seeing what's going on, even during shutdown
	ctx := context.WithoutCancel(c.internalCtx)

	traceID := trace.SpanContextFromContext(ctx).TraceID()
	spans, err := tracesClient.Subscribe(ctx, &enginetel.SubscribeRequest{
		TraceId:  traceID[:],
		ClientId: c.ID,
	})
	if err != nil {
		return fmt.Errorf("subscribe to spans: %w", err)
	}

	slog.Debug("exporting spans from engine")

	c.telemetry.Go(func() error {
		defer slog.Debug("done exporting spans from engine", "ctxErr", ctx.Err())

		for {
			data, err := spans.Recv()
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				return fmt.Errorf("recv spans: %w", err)
			}

			spans := telemetry.SpansFromPB(data.GetResourceSpans())

			slog.Debug("received spans from engine", "len", len(spans))

			for _, span := range spans {
				slog.Debug("received span from engine", "span", span.Name(), "id", span.SpanContext().SpanID(), "endTime", span.EndTime())
			}

			if err := c.Params.EngineTrace.ExportSpans(ctx, spans); err != nil {
				return fmt.Errorf("export %d spans: %w", len(spans), err)
			}
		}
	})

	return nil
}

func (c *Client) exportLogs(logsClient enginetel.LogsSourceClient) error {
	// NB: we never actually want to interrupt this, since it's relied upon for
	// seeing what's going on, even during shutdown
	ctx := context.WithoutCancel(c.internalCtx)

	traceID := trace.SpanContextFromContext(ctx).TraceID()
	logs, err := logsClient.Subscribe(ctx, &enginetel.SubscribeRequest{
		TraceId:  traceID[:],
		ClientId: c.ID,
	})
	if err != nil {
		return fmt.Errorf("subscribe to logs: %w", err)
	}

	slog.Debug("exporting logs from engine")

	c.telemetry.Go(func() error {
		defer slog.Debug("done exporting logs from engine", "ctxErr", ctx.Err())

		for {
			data, err := logs.Recv()
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				return fmt.Errorf("recv log: %w", err)
			}

			logs := telemetry.LogsFromPB(data.GetResourceLogs())

			slog.Debug("received logs from engine", "len", len(logs))

			if err := c.EngineLogs.Export(ctx, logs); err != nil {
				return fmt.Errorf("export %d logs: %w", len(logs), err)
			}
		}
	})

	return nil
}

func (c *Client) shutdownServer() error {
	// don't immediately cancel shutdown if we're shutting down because we were
	// canceled
	ctx := context.WithoutCancel(c.internalCtx)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "http://dagger"+engine.ShutdownEndpoint, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	if c.rootCtx.Err() != nil {
		req.URL.RawQuery = url.Values{
			"immediate": []string{"true"},
		}.Encode()
	}

	req.SetBasicAuth(c.SecretToken, "")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do shutdown: %w", err)
	}

	return resp.Body.Close()
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

func (c *Client) DialContext(ctx context.Context, _, _ string) (conn net.Conn, err error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}

	isNestedSession := c.nestedSessionPort != 0
	if isNestedSession {
		conn, err = (&net.Dialer{
			Cancel: ctx.Done(),
		}).Dial("tcp", "127.0.0.1:"+strconv.Itoa(c.nestedSessionPort))
	} else {
		conn, err = c.connector.Connect(ctx)
	}
	if err != nil {
		return nil, err
	}

	go func() {
		<-c.closeCtx.Done()
		cancel()
		conn.Close()
	}()
	return conn, nil
}

func (c *Client) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel, err := c.withClientCloseCancel(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("client has closed: " + err.Error()))
		return
	}
	r = r.WithContext(ctx)
	defer cancel()

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
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	_, err = io.Copy(w, resp.Body)
	if err != nil && !errors.Is(err, context.Canceled) {
		panic(err) // don't write header because we already wrote to the body, which isn't allowed
	}
}

func (c *Client) serveHijackedHTTP(ctx context.Context, cancel context.CancelFunc, w http.ResponseWriter, r *http.Request) {
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
		cancel()
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
	defer cancel()

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

const (
	// cache configs that should be applied to be import and export
	cacheConfigEnvName = "_EXPERIMENTAL_DAGGER_CACHE_CONFIG"
	// cache configs for imports only
	cacheImportsConfigEnvName = "_EXPERIMENTAL_DAGGER_CACHE_IMPORT_CONFIG"
	// cache configs for exports only
	cacheExportsConfigEnvName = "_EXPERIMENTAL_DAGGER_CACHE_EXPORT_CONFIG"
)

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
			configKVs = append(configKVs[:i+1], configKVs[i+2:]...)
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
	return engine.ClientMetadata{
		ClientID:                  c.ID,
		ClientVersion:             engine.Version,
		SessionID:                 c.SessionID,
		ClientSecretToken:         c.SecretToken,
		ClientHostname:            c.hostname,
		UpstreamCacheImportConfig: c.upstreamCacheImportOptions,
		UpstreamCacheExportConfig: c.upstreamCacheExportOptions,
		Labels:                    c.labels,
		CloudToken:                os.Getenv("DAGGER_CLOUD_TOKEN"),
		DoNotTrack:                analytics.DoNotTrack(),
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

type httpClient struct {
	inner       *http.Client
	headers     http.Header
	secretToken string
}

func (c *httpClient) Do(req *http.Request) (*http.Response, error) {
	for k, v := range c.headers {
		req.Header[k] = v
	}
	otel.GetTextMapPropagator().Inject(req.Context(), propagation.HeaderCarrier(req.Header))
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
	return ":mem:"
}

func (f DirectConn) Close() error {
	return nil
}
