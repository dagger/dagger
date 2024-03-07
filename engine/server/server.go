package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/containerd/containerd/defaults"
	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/cache"
	"github.com/dagger/dagger/telemetry/sdklog"
	"github.com/dagger/dagger/telemetry/sdklog/otlploghttp/transform"
	"github.com/dagger/dagger/tracing"
	"github.com/moby/buildkit/cache/remotecache"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	"github.com/sirupsen/logrus"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

type DaggerServer struct {
	serverID string
	traceID  trace.TraceID

	clientIDToSecretToken map[string]string
	connectedClients      int
	clientIDMu            sync.RWMutex

	// The metadata of client calls.
	// For the special case of the main client caller, the key is just empty string.
	// This is never explicitly deleted from; instead it will just be garbage collected
	// when this server for the session shuts down
	clientCallContext map[digest.Digest]*core.ClientCallContext
	clientCallMu      *sync.RWMutex

	// the http endpoints being served (as a map since APIs like shellEndpoint can add more)
	endpoints  map[string]http.Handler
	endpointMu *sync.RWMutex

	services *core.Services

	pubsub    *tracing.PubSub
	analytics analytics.Tracker

	doneCh    chan struct{}
	closeOnce sync.Once

	mainClientCallerID        string
	upstreamCacheExporterCfgs []bkgw.CacheOptionsEntry
	upstreamCacheExporters    map[string]remotecache.ResolveCacheExporterFunc
}

func (e *BuildkitController) newDaggerServer(ctx context.Context, clientMetadata *engine.ClientMetadata) (*DaggerServer, error) {
	s := &DaggerServer{
		serverID: clientMetadata.ServerID,

		clientIDToSecretToken: map[string]string{},
		clientCallContext:     map[digest.Digest]*core.ClientCallContext{},
		clientCallMu:          &sync.RWMutex{},
		endpoints:             map[string]http.Handler{},
		endpointMu:            &sync.RWMutex{},

		doneCh: make(chan struct{}, 1),

		pubsub: e.TelemetryPubSub,

		services: core.NewServices(),

		mainClientCallerID:     clientMetadata.ClientID,
		upstreamCacheExporters: e.UpstreamCacheExporters,
	}

	if traceID := trace.SpanContextFromContext(ctx).TraceID(); traceID.IsValid() {
		s.traceID = traceID
	} else {
		slog.Warn("invalid traceID", "traceID", traceID.String())
	}

	labels := clientMetadata.Labels.
		WithEngineLabel(e.EngineName).
		WithServerLabels(engine.Version, runtime.GOOS, runtime.GOARCH,
			e.cacheManager.ID() != cache.LocalCacheID)

	s.analytics = analytics.New(analytics.Config{
		DoNotTrack: clientMetadata.DoNotTrack || analytics.DoNotTrack(),
		Labels:     labels,
		CloudToken: clientMetadata.CloudToken,
	})

	getSessionCtx, getSessionCancel := context.WithTimeout(ctx, 10*time.Second)
	defer getSessionCancel()
	sessionCaller, err := e.SessionManager.Get(getSessionCtx, clientMetadata.ClientID, false)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	secretStore := core.NewSecretStore()
	authProvider := auth.NewRegistryAuthProvider()

	cacheImporterCfgs := make([]bkgw.CacheOptionsEntry, 0, len(clientMetadata.UpstreamCacheImportConfig))
	for _, cacheImportCfg := range clientMetadata.UpstreamCacheImportConfig {
		_, ok := e.UpstreamCacheImporters[cacheImportCfg.Type]
		if !ok {
			return nil, fmt.Errorf("unknown cache importer type %q", cacheImportCfg.Type)
		}
		cacheImporterCfgs = append(cacheImporterCfgs, bkgw.CacheOptionsEntry{
			Type:  cacheImportCfg.Type,
			Attrs: cacheImportCfg.Attrs,
		})
	}
	for _, cacheExportCfg := range clientMetadata.UpstreamCacheExportConfig {
		_, ok := e.UpstreamCacheExporters[cacheExportCfg.Type]
		if !ok {
			return nil, fmt.Errorf("unknown cache exporter type %q", cacheExportCfg.Type)
		}
		s.upstreamCacheExporterCfgs = append(s.upstreamCacheExporterCfgs, bkgw.CacheOptionsEntry{
			Type:  cacheExportCfg.Type,
			Attrs: cacheExportCfg.Attrs,
		})
	}

	root, err := core.NewRoot(ctx, core.QueryOpts{
		BuildkitOpts: &buildkit.Opts{
			Worker:                e.worker,
			SessionManager:        e.SessionManager,
			LLBSolver:             e.llbSolver,
			GenericSolver:         e.genericSolver,
			SecretStore:           secretStore,
			AuthProvider:          authProvider,
			PrivilegedExecEnabled: e.privilegedExecEnabled,
			UpstreamCacheImports:  cacheImporterCfgs,
			MainClientCaller:      sessionCaller,
			MainClientCallerID:    s.mainClientCallerID,
			DNSConfig:             e.DNSConfig,
			Frontends:             e.Frontends,
		},
		Services:          s.services,
		Platform:          core.Platform(e.worker.Platforms(true)[0]),
		Secrets:           secretStore,
		OCIStore:          e.worker.ContentStore(),
		LeaseManager:      e.worker.LeaseManager(),
		Auth:              authProvider,
		ClientCallContext: s.clientCallContext,
		ClientCallMu:      s.clientCallMu,
		Endpoints:         s.endpoints,
		EndpointMu:        s.endpointMu,
	})
	if err != nil {
		return nil, err
	}

	dag := dagql.NewServer(root)

	// stash away the cache so we can share it between other servers
	root.Cache = dag.Cache

	dag.Around(tracing.AroundFunc)

	coreMod := &schema.CoreMod{Dag: dag}
	root.DefaultDeps = core.NewModDeps(root, []core.Mod{coreMod})
	if err := coreMod.Install(ctx, dag); err != nil {
		return nil, err
	}

	// the main client caller starts out with the core API loaded
	s.clientCallContext[""] = &core.ClientCallContext{
		Deps: root.DefaultDeps,
		Root: root,
	}

	return s, nil
}

func (s *DaggerServer) ServeClientConn(
	ctx context.Context,
	clientMetadata *engine.ClientMetadata,
	conn net.Conn,
) error {
	bklog.G(ctx).Trace("serve client conn")
	defer bklog.G(ctx).Trace("done serving client conn")
	if err := s.VerifyClient(clientMetadata.ClientID, clientMetadata.ClientSecretToken); err != nil {
		return fmt.Errorf("failed to verify client: %w", err)
	}

	s.clientIDMu.Lock()
	s.connectedClients++
	defer func() {
		s.clientIDMu.Lock()
		s.connectedClients--
		s.clientIDMu.Unlock()
	}()
	s.clientIDMu.Unlock()

	conn = newLogicalDeadlineConn(splitWriteConn{nopCloserConn{conn}, defaults.DefaultMaxSendMsgSize * 95 / 100})
	l := &singleConnListener{conn: conn, closeCh: make(chan struct{})}
	go func() {
		<-ctx.Done()
		l.Close()
	}()

	httpSrv := http.Server{
		Handler:           s,
		ReadHeaderTimeout: 30 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			// FIXME(vito) not sure if this is right, being conservative and
			// respecting original context.Background(). later things added to ctx
			// might be redundant, or maybe we're OK with propagating cancellation
			// too (seems less likely considering how delicate draining events is).
			ctx := context.WithoutCancel(ctx)
			ctx = bklog.WithLogger(ctx, bklog.G(ctx))
			ctx = engine.ContextWithClientMetadata(ctx, clientMetadata)
			ctx = analytics.WithContext(ctx, s.analytics)
			return ctx
		},
	}
	defer httpSrv.Close()
	return httpSrv.Serve(l)
}

func (s *DaggerServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	errorOut := func(err error, code int) {
		bklog.G(ctx).WithError(err).Error("failed to serve request")
		http.Error(w, err.Error(), code)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		errorOut(err, http.StatusInternalServerError)
		return
	}

	callContext, ok := s.ClientCallContext(clientMetadata.ModuleCallerDigest)
	if !ok {
		errorOut(fmt.Errorf("client call %s not found", clientMetadata.ModuleCallerDigest), http.StatusInternalServerError)
		return
	}

	schema, err := callContext.Deps.Schema(ctx)
	if err != nil {
		// TODO: technically this is not *always* bad request, should ideally be more specific and differentiate
		errorOut(err, http.StatusBadRequest)
		return
	}

	defer func() {
		if v := recover(); v != nil {
			bklog.G(context.TODO()).Errorf("panic serving schema: %v %s", v, string(debug.Stack()))
			// check whether this is a hijacked connection, if so we can't write any http errors to it
			_, err := w.Write(nil)
			if err == http.ErrHijacked {
				return
			}
			gqlErr := &gqlerror.Error{
				Message: "Internal Server Error",
			}
			code := http.StatusInternalServerError
			switch v := v.(type) {
			case error:
				gqlErr.Err = v
				gqlErr.Message = v.Error()
			case string:
				gqlErr.Message = v
			}
			res := graphql.Response{
				Errors: gqlerror.List{gqlErr},
			}
			bytes, err := json.Marshal(res)
			if err != nil {
				panic(err)
			}
			http.Error(w, string(bytes), code)
		}
	}()

	srv := handler.NewDefaultServer(schema)
	// NB: break glass when needed:
	// srv.AroundResponses(func(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	// 	res := next(ctx)
	// 	pl, err := json.Marshal(res)
	// 	slog.Debug("graphql response", "response", string(pl), "error", err)
	// 	return res
	// })
	mux := http.NewServeMux()

	mux.Handle("/query", srv)

	mux.Handle("/shutdown", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()

		slog.Debug("shutting down server",
			"serverID", s.serverID,
			"traceID", s.traceID,
			"clientID", clientMetadata.ClientID,
			"mainClientID", s.mainClientCallerID,
			"callerID", clientMetadata.ModuleCallerDigest)

		if clientMetadata.ClientID == s.mainClientCallerID {
			// Detach all services associated with the server, which will only
			// synchronously shut them down if we're the last binder.
			s.services.DetachServerServices(ctx, s.serverID)

			if len(s.upstreamCacheExporterCfgs) > 0 {
				bklog.G(ctx).Debugf("running cache export for client %s", clientMetadata.ClientID)
				cacheExporterFuncs := make([]buildkit.ResolveCacheExporterFunc, len(s.upstreamCacheExporterCfgs))
				for i, cacheExportCfg := range s.upstreamCacheExporterCfgs {
					cacheExportCfg := cacheExportCfg
					cacheExporterFuncs[i] = func(ctx context.Context, sessionGroup session.Group) (remotecache.Exporter, error) {
						exporterFunc, ok := s.upstreamCacheExporters[cacheExportCfg.Type]
						if !ok {
							return nil, fmt.Errorf("unknown cache exporter type %q", cacheExportCfg.Type)
						}
						return exporterFunc(ctx, sessionGroup, cacheExportCfg.Attrs)
					}
				}
				s.clientCallMu.RLock()
				bk := s.clientCallContext[""].Root.Buildkit
				s.clientCallMu.RUnlock()
				err := bk.UpstreamCacheExport(ctx, cacheExporterFuncs)
				if err != nil {
					bklog.G(ctx).WithError(err).Errorf("error running cache export for client %s", clientMetadata.ClientID)
				}
				bklog.G(ctx).Debugf("done running cache export for client %s", clientMetadata.ClientID)
			}
		}

		// Flush all in-flight telemetry when a client shuts down.
		//
		// Awkwardly, we're flushing in-flight telemetry for _all_ clients when
		// _each_ client goes away. But we're only flushing the PubSub exporter,
		// which doesn't seem expensive enough to be worth the added complexity
		// of somehow only flushing a single client. This exporter already
		// flushes every 100ms anyway, so this really just helps ensure the last
		// few spans are received.
		tracing.FlushLiveProcessors(ctx)
	}))

	mux.Handle("/trace", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusNotImplemented)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		exp, err := otlptrace.New(ctx, &chunkedTraceClient{
			w: w,
			f: flusher,
		})
		if err != nil {
			slog.Warn("failed to create exporter", "err", err)
			http.Error(w, "failed to create exporter", http.StatusInternalServerError)
			return
		}
		s.pubsub.SubscribeToSpans(ctx, s.traceID, exp)
	}))

	mux.Handle("/logs", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusNotImplemented)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		exp := &chunkedLogsClient{
			w: w,
			f: flusher,
		}
		if err := exp.Start(ctx); err != nil {
			slog.Warn("failed to emit first chunk", "err", err)
			return
		}
		s.pubsub.SubscribeToLogs(ctx, s.traceID, exp)
	}))

	s.endpointMu.RLock()
	for path, handler := range s.endpoints {
		mux.Handle(path, handler)
	}
	s.endpointMu.RUnlock()

	r = r.WithContext(ctx)

	var handler http.Handler = mux
	handler = flushAfterNBytes(buildkit.MaxFileContentsChunkSize)(handler)
	handler.ServeHTTP(w, r)
}

func (s *DaggerServer) RegisterClient(clientID, clientHostname, secretToken string) error {
	s.clientIDMu.Lock()
	defer s.clientIDMu.Unlock()
	existingToken, ok := s.clientIDToSecretToken[clientID]
	if ok {
		if existingToken != secretToken {
			return fmt.Errorf("client ID %q already registered with different secret token", clientID)
		}
		return nil
	}
	s.clientIDToSecretToken[clientID] = secretToken
	// NOTE: we purposely don't delete the secret token, it should never be reused and will be released
	// from memory once the dagger server instance corresponding to this buildkit client shuts down.
	// Deleting it would make it easier to create race conditions around using the client's session
	// before it is fully closed.

	return nil
}

func (s *DaggerServer) VerifyClient(clientID, secretToken string) error {
	s.clientIDMu.RLock()
	defer s.clientIDMu.RUnlock()
	existingToken, ok := s.clientIDToSecretToken[clientID]
	if !ok {
		return fmt.Errorf("client ID %q not registered", clientID)
	}
	if existingToken != secretToken {
		return fmt.Errorf("client ID %q registered with different secret token", clientID)
	}
	return nil
}

func (s *DaggerServer) ClientCallContext(clientDigest digest.Digest) (*core.ClientCallContext, bool) {
	s.clientCallMu.RLock()
	defer s.clientCallMu.RUnlock()
	ctx, ok := s.clientCallContext[clientDigest]
	return ctx, ok
}

func (s *DaggerServer) CurrentServedDeps(ctx context.Context) (*core.ModDeps, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, ok := s.ClientCallContext(clientMetadata.ModuleCallerDigest)
	if !ok {
		return nil, fmt.Errorf("client call %s not found", clientMetadata.ModuleCallerDigest)
	}
	return callCtx.Deps, nil
}

func (s *DaggerServer) LogMetrics(l *logrus.Entry) *logrus.Entry {
	s.clientIDMu.RLock()
	defer s.clientIDMu.RUnlock()
	return l.WithField(fmt.Sprintf("server-%s-client-count", s.serverID), s.connectedClients)
}

func (s *DaggerServer) Close(ctx context.Context) error {
	defer s.closeOnce.Do(func() {
		close(s.doneCh)
	})

	var err error

	if err := s.services.StopClientServices(ctx, s.serverID); err != nil {
		slog.Error("failed to stop client services", "error", err)
	}

	s.clientCallMu.RLock()
	for _, callCtx := range s.clientCallContext {
		err = errors.Join(err, callCtx.Root.Buildkit.Close())
	}
	s.clientCallMu.RUnlock()

	// close the analytics recorder
	err = errors.Join(err, s.analytics.Close())

	return err
}

func (s *DaggerServer) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.doneCh:
		return nil
	}
}

type splitWriteConn struct {
	net.Conn
	maxMsgSize int
}

func (r splitWriteConn) Write(b []byte) (n int, err error) {
	for {
		if len(b) == 0 {
			return
		}

		var bnext []byte
		if len(b) > r.maxMsgSize {
			b, bnext = b[:r.maxMsgSize], b[r.maxMsgSize:]
		}

		n2, err := r.Conn.Write(b)
		n += n2
		if err != nil {
			return n, err
		}

		b = bnext
	}
}

type chunkedTraceClient struct {
	w http.ResponseWriter
	f http.Flusher
	l sync.Mutex
}

var _ otlptrace.Client = (*chunkedTraceClient)(nil)

func (h *chunkedTraceClient) Start(ctx context.Context) error {
	slog.Info("attached to traces; sending initial response")
	fmt.Fprintf(h.w, "0\n")
	h.f.Flush()
	return nil
}

func (h *chunkedTraceClient) Stop(ctx context.Context) error {
	return nil
}

func (h *chunkedTraceClient) UploadTraces(ctx context.Context, protoSpans []*tracepb.ResourceSpans) error {
	h.l.Lock()
	defer h.l.Unlock()
	pbRequest := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: protoSpans,
	}
	rawRequest, err := proto.Marshal(pbRequest)
	if err != nil {
		return err
	}
	// TODO hacky length-prefixed encoding
	fmt.Fprintf(h.w, "%d\n", len(rawRequest))
	h.w.Write(rawRequest)
	h.f.Flush()
	return nil
}

type chunkedLogsClient struct {
	w http.ResponseWriter
	f http.Flusher
	l sync.Mutex
}

func (h *chunkedLogsClient) Start(ctx context.Context) error {
	slog.Info("attached to traces; sending initial response")
	fmt.Fprintf(h.w, "0\n")
	h.f.Flush()
	return nil
}

var _ sdklog.LogExporter = (*chunkedLogsClient)(nil)

func (h *chunkedLogsClient) ExportLogs(ctx context.Context, logs []*sdklog.LogData) error {
	h.l.Lock()
	defer h.l.Unlock()
	pbRequest := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: transform.Logs(logs),
	}
	rawRequest, err := proto.Marshal(pbRequest)
	if err != nil {
		return err
	}
	// TODO hacky length-prefixed encoding
	fmt.Fprintf(h.w, "%d\n", len(rawRequest))
	h.w.Write(rawRequest)
	h.f.Flush()
	return nil
}

func (h *chunkedLogsClient) Shutdown(ctx context.Context) error {
	return nil
}
