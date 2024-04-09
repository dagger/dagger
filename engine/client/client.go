package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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
	"github.com/moby/buildkit/session/filesync"
	"github.com/moby/buildkit/session/grpchijack"
	"github.com/moby/buildkit/util/grpcerrors"
	"github.com/opencontainers/go-digest"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"dagger.io/dagger"
	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/drivers"
	"github.com/dagger/dagger/engine/session"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/telemetry"
	"github.com/dagger/dagger/telemetry/sdklog"
)

type Params struct {
	// The id of the server to connect to, or if blank a new one
	// should be started.
	ServerID string

	SecretToken string

	RunnerHost string // host of dagger engine runner serving buildkit apis
	UserAgent  string

	DisableHostRW bool

	EngineNameCallback func(string)
	CloudURLCallback   func(string)

	// If this client is for a module function, this digest will be set in the
	// grpc context metadata for any api requests back to the engine. It's used by the API
	// server to determine which schema to serve and other module context metadata.
	ModuleCallerDigest digest.Digest

	EngineTrace sdktrace.SpanExporter
	EngineLogs  sdklog.LogExporter
}

type Client struct {
	Params

	rootCtx context.Context

	eg *errgroup.Group

	internalCtx    context.Context
	internalCancel context.CancelFunc

	closeCtx      context.Context
	closeRequests context.CancelFunc
	closeMu       sync.RWMutex

	telemetry     *errgroup.Group
	telemetryConn *grpc.ClientConn

	httpClient *http.Client
	bkClient   *bkclient.Client
	bkSession  *bksession.Session

	// A client for the dagger API that is directly hooked up to this engine client.
	// Currently used for the dagger CLI so it can avoid making a subprocess of itself...
	daggerClient *dagger.Client

	upstreamCacheImportOptions []*controlapi.CacheOptionsEntry
	upstreamCacheExportOptions []*controlapi.CacheOptionsEntry

	hostname string

	nestedSessionPort int

	labels telemetry.Labels
}

func Connect(ctx context.Context, params Params) (_ *Client, _ context.Context, rerr error) {
	c := &Client{Params: params}
	if c.SecretToken == "" {
		c.SecretToken = uuid.New().String()
	}
	if c.ServerID == "" {
		c.ServerID = identity.NewID()
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

	c.labels = telemetry.LoadDefaultLabels(workdir, engine.Version)

	nestedSessionPortVal, isNestedSession := os.LookupEnv("DAGGER_SESSION_PORT")
	if isNestedSession {
		nestedSessionPort, err := strconv.Atoi(nestedSessionPortVal)
		if err != nil {
			return nil, nil, fmt.Errorf("parse DAGGER_SESSION_PORT: %w", err)
		}
		c.nestedSessionPort = nestedSessionPort
		c.SecretToken = os.Getenv("DAGGER_SESSION_TOKEN")
		c.httpClient = &http.Client{
			Transport: &http.Transport{
				DialContext:       c.DialContext,
				DisableKeepAlives: true,
			},
		}
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

	connectSpanOpts := []trace.SpanStartOption{
		telemetry.Encapsulate(),
	}

	if c.Params.ModuleCallerDigest != "" {
		connectSpanOpts = append(connectSpanOpts, telemetry.Internal())
	}

	// NB: don't propagate this ctx, we don't want everything tucked beneath connect
	connectCtx, span := Tracer().Start(ctx, "connect", connectSpanOpts...)
	defer telemetry.End(span, func() error { return rerr })

	if err := c.startEngine(connectCtx); err != nil {
		return nil, nil, fmt.Errorf("start engine: %w", err)
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
			c.bkSession.Close()
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

	connector, err := driver.Provision(ctx, remote, &drivers.DriverOpts{
		UserAgent:        c.UserAgent,
		DaggerCloudToken: cloudToken,
		GPUSupport:       os.Getenv(drivers.EnvGPUSupport),
	})
	if err != nil {
		return err
	}

	if err := retry(ctx, 10*time.Millisecond, func(elapsed time.Duration, ctx context.Context) error {
		// Open a separate connection for telemetry.
		telemetryConn, err := grpc.DialContext(c.internalCtx, remote.String(),
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
				return connector.Connect(c.internalCtx)
			}),
			// Same defaults as Buildkit. I hit the default 4MB limit pretty quickly.
			// Shrinking IDs might help.
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(defaults.DefaultMaxRecvMsgSize)),
			grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(defaults.DefaultMaxSendMsgSize)),
			// Uncomment to measure telemetry traffic.
			// grpc.WithUnaryInterceptor(telemetry.MeasuringUnaryClientInterceptor()),
			// grpc.WithStreamInterceptor(telemetry.MeasuringStreamClientInterceptor()),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("telemetry grpc dial: %w", err)
		}
		c.telemetryConn = telemetryConn
		c.telemetry = new(errgroup.Group)

		if c.EngineTrace != nil {
			if err := c.exportTraces(telemetry.NewTracesSourceClient(telemetryConn)); err != nil {
				return fmt.Errorf("export traces: %w", err)
			}
		}

		if c.EngineLogs != nil {
			if err := c.exportLogs(telemetry.NewLogsSourceClient(telemetryConn)); err != nil {
				return fmt.Errorf("export logs: %w", err)
			}
		}

		return nil
	}); err != nil {
		return fmt.Errorf("attach to telemetry: %w", err)
	}

	bkClient, bkInfo, err := newBuildkitClient(ctx, remote, connector)
	if err != nil {
		return fmt.Errorf("new client: %w", err)
	}

	c.bkClient = bkClient

	if c.EngineNameCallback != nil {
		engineName := fmt.Sprintf("%s (version %s)", bkInfo.BuildkitVersion.Revision, bkInfo.BuildkitVersion.Version)
		c.EngineNameCallback(engineName)
	}

	return nil
}

func (c *Client) startSession(ctx context.Context) (rerr error) {
	ctx, sessionSpan := Tracer().Start(ctx, "starting session")
	defer telemetry.End(sessionSpan, func() error { return rerr })

	ctx, stdout, stderr := telemetry.WithStdioToOtel(ctx, InstrumentationLibrary)

	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("get hostname: %w", err)
	}
	c.hostname = hostname

	sharedKey := ""
	bkSession, err := bksession.NewSession(ctx, identity.NewID(), sharedKey)
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	c.bkSession = bkSession
	defer func() {
		if rerr != nil {
			c.bkSession.Close()
		}
	}()

	c.internalCtx = engine.ContextWithClientMetadata(c.internalCtx, &engine.ClientMetadata{
		ClientID:           c.ID(),
		ClientSecretToken:  c.SecretToken,
		ServerID:           c.ServerID,
		ClientHostname:     c.hostname,
		Labels:             c.labels,
		ModuleCallerDigest: c.ModuleCallerDigest,
	})

	// filesync
	if !c.DisableHostRW {
		bkSession.Allow(AnyDirSource{})
		bkSession.Allow(AnyDirTarget{})
	}

	// sockets
	bkSession.Allow(SocketProvider{
		EnableHostNetworkAccess: !c.DisableHostRW,
	})

	// registry auth
	bkSession.Allow(authprovider.NewDockerAuthProvider(config.LoadDefaultConfigFile(os.Stderr), nil))

	// host=>container networking
	bkSession.Allow(session.NewTunnelListenerAttachable(ctx))

	// connect to the server, registering our session attachables and starting the server if not
	// already started
	c.eg.Go(func() error {
		return bkSession.Run(c.internalCtx, func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
			return grpchijack.Dialer(c.bkClient.ControlClient())(ctx, proto, engine.ClientMetadata{
				RegisterClient:            true,
				ClientID:                  c.ID(),
				ClientSecretToken:         c.SecretToken,
				ServerID:                  c.ServerID,
				ClientHostname:            hostname,
				UpstreamCacheImportConfig: c.upstreamCacheImportOptions,
				UpstreamCacheExportConfig: c.upstreamCacheExportOptions,
				Labels:                    c.labels,
				ModuleCallerDigest:        c.ModuleCallerDigest,
				CloudToken:                os.Getenv("DAGGER_CLOUD_TOKEN"),
				DoNotTrack:                analytics.DoNotTrack(),
			}.AppendToMD(meta))
		})
	})

	// Try connecting to the session server to make sure it's running
	c.httpClient = &http.Client{Transport: &http.Transport{
		DialContext: c.DialContext,
		// connection re-use in combination with the underlying grpc stream makes
		// managing the lifetime of connections very confusing, so disabling for now
		// TODO: For performance, it would be better to figure out a way to re-enable this
		DisableKeepAlives: true,
	}}

	// there are fast retries server-side so we can start out with a large interval here
	if err := retry(ctx, 3*time.Second, func(elapsed time.Duration, ctx context.Context) error {
		// Make an introspection request, since those get ignored by telemetry and
		// we don't want this to show up, since it's just a health check.
		err := c.Do(ctx, `{__schema{description}}`, "", nil, nil)
		if err != nil {
			// only show errors once the time between attempts exceeds this threshold, otherwise common
			// cases of 1 or 2 retries become too noisy
			if elapsed > time.Second {
				fmt.Fprintln(stderr, "Failed to connect; retrying...", err)
			}
		} else {
			fmt.Fprintln(stdout, "OK!")
		}
		return err
	}); err != nil {
		return fmt.Errorf("poll for session: %w", err)
	}

	return nil
}

func retry(ctx context.Context, initialInterval time.Duration, fn func(time.Duration, context.Context) error) error {
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = initialInterval
	connectRetryCtx, connectRetryCancel := context.WithTimeout(ctx, 300*time.Second)
	defer connectRetryCancel()
	start := time.Now()
	return backoff.Retry(func() error {
		if ctx.Err() != nil {
			return backoff.Permanent(ctx.Err())
		}
		nextBackoff := bo.NextBackOff()
		ctx, cancel := context.WithTimeout(connectRetryCtx, nextBackoff)
		defer cancel()
		return fn(time.Since(start), ctx)
	}, backoff.WithContext(bo, connectRetryCtx))
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
		c.eg.Go(func() error {
			c.httpClient.CloseIdleConnections()
			return nil
		})
	}

	if c.bkSession != nil {
		c.eg.Go(c.bkSession.Close)
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

func (c *Client) exportTraces(tracesClient telemetry.TracesSourceClient) error {
	// NB: we never actually want to interrupt this, since it's relied upon for
	// seeing what's going on, even during shutdown
	ctx := context.WithoutCancel(c.internalCtx)

	traceID := trace.SpanContextFromContext(ctx).TraceID()
	spans, err := tracesClient.Subscribe(ctx, &telemetry.TelemetryRequest{
		TraceId: traceID[:],
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
				return fmt.Errorf("recv log: %w", err)
			}

			spans := telemetry.SpansFromProto(data.GetResourceSpans())

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

func (c *Client) exportLogs(logsClient telemetry.LogsSourceClient) error {
	// NB: we never actually want to interrupt this, since it's relied upon for
	// seeing what's going on, even during shutdown
	ctx := context.WithoutCancel(c.internalCtx)

	traceID := trace.SpanContextFromContext(ctx).TraceID()
	logs, err := logsClient.Subscribe(ctx, &telemetry.TelemetryRequest{
		TraceId: traceID[:],
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

			logs := telemetry.TransformPBLogs(data.GetResourceLogs())

			slog.Debug("received logs from engine", "len", len(logs))

			if err := c.EngineLogs.ExportLogs(ctx, logs); err != nil {
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

	req, err := http.NewRequestWithContext(ctx, "POST", "http://dagger/shutdown", nil)
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

func (c *Client) ID() string {
	return c.bkSession.ID()
}

func (c *Client) DialContext(ctx context.Context, _, _ string) (conn net.Conn, err error) {
	// NOTE: the context given to grpchijack.Dialer is for the lifetime of the stream.
	// If http connection re-use is enabled, that can be far past this DialContext call.
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}

	isNestedSession := c.nestedSessionPort != 0
	if isNestedSession {
		conn, err = (&net.Dialer{
			Cancel:    ctx.Done(),
			KeepAlive: -1, // disable for now
		}).Dial("tcp", "127.0.0.1:"+strconv.Itoa(c.nestedSessionPort))
	} else {
		conn, err = grpchijack.Dialer(c.bkClient.ControlClient())(ctx, "", engine.ClientMetadata{
			ClientID:           c.ID(),
			ClientSecretToken:  c.SecretToken,
			ServerID:           c.ServerID,
			ClientHostname:     c.hostname,
			Labels:             c.labels,
			ModuleCallerDigest: c.ModuleCallerDigest,
		}.ToGRPCMD())
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

	gqlClient := graphql.NewClient("http://dagger/query", doerWithHeaders{
		inner: c.httpClient,
		headers: http.Header{
			"Authorization": []string{"Basic " + base64.StdEncoding.EncodeToString([]byte(c.SecretToken+":"))},
		},
	})

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

// Local dir imports
type AnyDirSource struct{}

func (s AnyDirSource) Register(server *grpc.Server) {
	filesync.RegisterFileSyncServer(server, s)
}

func (s AnyDirSource) TarStream(stream filesync.FileSync_TarStreamServer) error {
	return fmt.Errorf("tarstream not supported")
}

func (s AnyDirSource) DiffCopy(stream filesync.FileSync_DiffCopyServer) error {
	opts, err := engine.LocalImportOptsFromContext(stream.Context())
	if err != nil {
		return fmt.Errorf("get local import opts: %w", err)
	}

	if opts.ReadSingleFileOnly {
		// just stream the file bytes to the caller
		fileContents, err := os.ReadFile(opts.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return status.Errorf(codes.NotFound, "stat path: %s", err)
			}
			return fmt.Errorf("read file: %w", err)
		}
		if len(fileContents) > int(opts.MaxFileSize) {
			// NOTE: can lift this size restriction by chunking if ever needed
			return fmt.Errorf("file contents too large: %d > %d", len(fileContents), opts.MaxFileSize)
		}
		return stream.SendMsg(&filesync.BytesMessage{Data: fileContents})
	}

	if opts.StatPathOnly {
		stat, err := fsutil.Stat(opts.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return status.Errorf(codes.NotFound, "stat path: %s", err)
			}
			return fmt.Errorf("stat path: %w", err)
		}
		if opts.StatReturnAbsPath {
			stat.Path, err = filepath.Abs(opts.Path)
			if err != nil {
				return fmt.Errorf("get abs path: %w", err)
			}
		}
		stat.Path = filepath.ToSlash(stat.Path)
		return stream.SendMsg(stat)
	}

	// otherwise, do the whole directory sync back to the caller
	fs, err := fsutil.NewFS(opts.Path)
	if err != nil {
		return err
	}
	fs, err = fsutil.NewFilterFS(fs, &fsutil.FilterOpt{
		IncludePatterns: opts.IncludePatterns,
		ExcludePatterns: opts.ExcludePatterns,
		FollowPaths:     opts.FollowPaths,
		Map: func(p string, st *fstypes.Stat) fsutil.MapResult {
			st.Uid = 0
			st.Gid = 0
			return fsutil.MapResultKeep
		},
	})
	if err != nil {
		return err
	}
	return fsutil.Send(stream.Context(), stream, fs, nil)
}

// Local dir exports
type AnyDirTarget struct{}

func (t AnyDirTarget) Register(server *grpc.Server) {
	filesync.RegisterFileSendServer(server, t)
}

func (AnyDirTarget) DiffCopy(stream filesync.FileSend_DiffCopyServer) (rerr error) {
	opts, err := engine.LocalExportOptsFromContext(stream.Context())
	if err != nil {
		return fmt.Errorf("get local export opts: %w", err)
	}

	if !opts.IsFileStream {
		// we're writing a full directory tree, normal fsutil.Receive is good
		if err := os.MkdirAll(filepath.FromSlash(opts.Path), 0o700); err != nil {
			return fmt.Errorf("failed to create synctarget dest dir %s: %w", opts.Path, err)
		}

		err := fsutil.Receive(stream.Context(), stream, opts.Path, fsutil.ReceiveOpt{
			Merge: opts.Merge,
			Filter: func(path string, stat *fstypes.Stat) bool {
				stat.Uid = uint32(os.Getuid())
				stat.Gid = uint32(os.Getgid())
				return true
			},
		})
		if err != nil {
			return fmt.Errorf("failed to receive fs changes: %w", err)
		}
		return nil
	}

	// This is either a file export or a container tarball export, we'll just be receiving BytesMessages with
	// the contents and can write them directly to the destination path.

	// If the dest is a directory that already exists, we will never delete it and replace it with the file.
	// However, if allowParentDirPath is set, we will write the file underneath that existing directory.
	// But if allowParentDirPath is not set, which is the default setting in our API right now, we will return
	// an error when path is a pre-existing directory.
	allowParentDirPath := opts.AllowParentDirPath

	// File exports specifically (as opposed to container tar exports) have an original filename that we will
	// use in the case where dest is a directory and allowParentDirPath is set, in which case we need to know
	// what to name the file underneath the pre-existing directory.
	fileOriginalName := opts.FileOriginalName

	var destParentDir string
	var finalDestPath string
	stat, err := os.Lstat(opts.Path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// we are writing the file to a new path
		destParentDir = filepath.Dir(opts.Path)
		finalDestPath = opts.Path
	case err != nil:
		// something went unrecoverably wrong if stat failed and it wasn't just because the path didn't exist
		return fmt.Errorf("failed to stat synctarget dest %s: %w", opts.Path, err)
	case !stat.IsDir():
		// we are overwriting an existing file
		destParentDir = filepath.Dir(opts.Path)
		finalDestPath = opts.Path
	case !allowParentDirPath:
		// we are writing to an existing directory, but allowParentDirPath is not set, so fail
		return fmt.Errorf("destination %q is a directory; must be a file path unless allowParentDirPath is set", opts.Path)
	default:
		// we are writing to an existing directory, and allowParentDirPath is set,
		// so write the file under the directory using the same file name as the source file
		if fileOriginalName == "" {
			// NOTE: we could instead just default to some name like container.tar or something if desired
			return fmt.Errorf("cannot export container tar to existing directory %q", opts.Path)
		}
		destParentDir = opts.Path
		finalDestPath = filepath.Join(destParentDir, fileOriginalName)
	}

	if err := os.MkdirAll(destParentDir, 0o700); err != nil {
		return fmt.Errorf("failed to create synctarget dest dir %s: %w", destParentDir, err)
	}

	if opts.FileMode == 0 {
		opts.FileMode = 0o600
	}
	destF, err := os.OpenFile(finalDestPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, opts.FileMode)
	if err != nil {
		return fmt.Errorf("failed to create synctarget dest file %s: %w", finalDestPath, err)
	}
	defer destF.Close()
	for {
		msg := filesync.BytesMessage{}
		if err := stream.RecvMsg(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if _, err := destF.Write(msg.Data); err != nil {
			return err
		}
	}
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

type doerWithHeaders struct {
	inner   graphql.Doer
	headers http.Header
}

func (d doerWithHeaders) Do(req *http.Request) (*http.Response, error) {
	for k, v := range d.headers {
		req.Header[k] = v
	}
	return d.inner.Do(req)
}

func EngineConn(engineClient *Client) DirectConn {
	return func(req *http.Request) (*http.Response, error) {
		req.SetBasicAuth(engineClient.SecretToken, "")
		resp := httptest.NewRecorder()
		engineClient.ServeHTTP(resp, req)
		return resp.Result(), nil
	}
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
