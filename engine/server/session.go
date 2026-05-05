package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	bkgw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/internal/buildkit/util/flightcontrol"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	telemetry "github.com/dagger/otel-go"
	"github.com/sirupsen/logrus"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"resenje.org/singleflight"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	engineclient "github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/engineutil"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/util/cleanups"
)

type daggerSession struct {
	sessionID          string
	mainClientCallerID string

	state   daggerSessionState
	stateMu sync.RWMutex

	clients  map[string]*daggerClient // clientID -> client
	clientMu sync.RWMutex

	closingCtx       context.Context
	cancelClosing    context.CancelCauseFunc
	closeClosingOnce sync.Once

	// closed after the shutdown endpoint is called
	shutdownCh        chan struct{}
	closeShutdownOnce sync.Once

	// the http endpoints being served (as a map since APIs like shellEndpoint can add more)
	endpoints  map[string]http.Handler
	endpointMu sync.RWMutex

	// informed when a client goes away to prevent hanging on drain
	telemetryPubSub *PubSub
	seenKeys        sync.Map

	services *core.Services
	resolver *serverresolver.Resolver

	analytics analytics.Tracker

	authProvider *auth.RegistryAuthProvider

	containers   map[bkgw.Container]struct{}
	containersMu sync.Mutex

	dagqlMu       sync.Mutex
	dagqlCond     *sync.Cond
	dagqlInFlight int
	dagqlClosing  bool

	interactive        bool
	interactiveCommand []string

	allowedLLMModules []string

	lockFiles  map[workspaceLockKey]*workspaceLockState
	lockFileMu sync.RWMutex
}

type workspaceLockKey struct {
	ownerClientID string
	lockPath      string
}

type workspaceLockState struct {
	ws       *core.Workspace
	lockPath string
	lock     *workspace.Lock
	delta    *workspace.Lock
	loaded   bool
	dirty    bool
}

type daggerSessionState string

const (
	sessionStateUninitialized daggerSessionState = "uninitialized"
	sessionStateInitialized   daggerSessionState = "initialized"
	sessionStateRemoved       daggerSessionState = "removed"
)

type daggerClient struct {
	daggerSession  *daggerSession
	clientID       string
	clientVersion  string
	secretToken    string
	clientMetadata *engine.ClientMetadata

	// closed after the shutdown endpoint is called
	shutdownCh        chan struct{}
	closeShutdownOnce sync.Once

	// if the client is a nested client, this is its ancestral clients,
	// with the most recent parent last
	parents []*daggerClient

	state   daggerClientState
	stateMu sync.RWMutex
	// the number of active http requests to any endpoint from this client,
	// used to determine when to cleanup the client+session
	activeCount int

	dag       *dagql.Server
	dagqlRoot *core.Query

	// if the client is coming from a module, this is that module
	mod dagql.ObjectResult[*core.Module]

	// the set of modules being served to this client, with per-module
	// install policy (constructor vs type-only)
	servedMods *core.SchemaBuilder
	// the default deps that each client/module starts out with (currently just core)
	defaultDeps *core.SchemaBuilder

	// If the client is itself from a function call in a user module, this is set with the
	// metadata of that ongoing function call
	fnCall *core.FunctionCall

	// If the client is executing in an Env context, this is that Env.
	env dagql.ObjectResult[*core.Env]

	// engine utility job-related state/config
	getClientCaller  func(string) (bksession.Caller, error)
	dialer           *net.Dialer
	engineUtilClient *engineutil.Client

	// SQLite database storing telemetry + anything else
	tracerProvider *sdktrace.TracerProvider
	spanExporter   sdktrace.SpanExporter

	loggerProvider *sdklog.LoggerProvider
	logExporter    sdklog.Exporter

	meterProvider  *sdkmetric.MeterProvider
	metricExporter sdkmetric.Exporter

	// Workspace and extra module loading is deferred from initializeDaggerClient
	// to serveQuery because it requires the client's engine utility session, which
	// isn't available during initialization (the session attachables request
	// is blocked on the same locks that initializeDaggerClient holds).

	// Whether this client should detect its own workspace binding.
	// Non-module clients detect their own workspace; module clients inherit a
	// parent workspace binding instead.
	pendingWorkspaceLoad bool
	workspaceMu          sync.Mutex
	workspaceLoaded      bool
	workspaceErr         error

	// Cached workspace result from ensureWorkspaceLoaded.
	workspace *core.Workspace

	pendingModules      []pendingModule      // gathered in detectAndLoadWorkspaceWithRootfs
	pendingExtraModules []engine.ExtraModule // populated from clientMD, can arrive late
	modulesMu           sync.Mutex
	modulesLoaded       bool
	modulesErr          error

	// NOTE: do not use this field directly as it may not be open
	// after the client has shutdown; use TelemetryDB() instead
	// This field exists to "keepalive" the db while the client
	// is around to avoid perf overhead of closing/reopening a lot
	keepAliveTelemetryDB *clientdb.DB
}

func (srv *Server) getCoreSchemaBase(ctx context.Context) (*schema.CoreSchemaBase, error) {
	srv.coreSchemaBaseMu.Lock()
	defer srv.coreSchemaBaseMu.Unlock()

	if srv.coreSchemaBase != nil {
		return srv.coreSchemaBase, nil
	}

	base, err := schema.NewCoreSchemaBase(ctx, srv)
	if err != nil {
		return nil, err
	}
	srv.coreSchemaBase = base
	return base, nil
}

type daggerClientState string

const (
	clientStateUninitialized daggerClientState = "uninitialized"
	clientStateInitialized   daggerClientState = "initialized"
)

func (client *daggerClient) String() string {
	return fmt.Sprintf("<Client %s: %s>", client.clientID, client.state)
}

// NOTE: be sure to defer closing the DB when done with it, otherwise it may leak
func (client *daggerClient) TelemetryDB(ctx context.Context) (*clientdb.DB, error) {
	return client.daggerSession.telemetryPubSub.srv.clientDBs.Open(ctx, client.clientID)
}

func (client *daggerClient) FlushTelemetry(ctx context.Context) error {
	slog := slog.With("client", client.clientID)
	var errs error
	if client.tracerProvider != nil {
		slog.ExtraDebug("force flushing client traces")
		errs = errors.Join(errs, client.tracerProvider.ForceFlush(ctx))
	}
	if client.loggerProvider != nil {
		slog.ExtraDebug("force flushing client logs")
		errs = errors.Join(errs, client.loggerProvider.ForceFlush(ctx))
	}
	if client.meterProvider != nil {
		slog.ExtraDebug("force flushing client metrics")
		errs = errors.Join(errs, client.meterProvider.ForceFlush(ctx))
	}
	return errs
}

func (client *daggerClient) ShutdownTelemetry(ctx context.Context) error {
	slog := slog.With("client", client.clientID)
	var errs error
	if client.tracerProvider != nil {
		slog.ExtraDebug("force flushing client traces")
		errs = errors.Join(errs, client.tracerProvider.Shutdown(ctx))
	}
	if client.loggerProvider != nil {
		slog.ExtraDebug("force flushing client logs")
		errs = errors.Join(errs, client.loggerProvider.Shutdown(ctx))
	}
	if client.meterProvider != nil {
		slog.ExtraDebug("force flushing client metrics")
		errs = errors.Join(errs, client.meterProvider.Shutdown(ctx))
	}
	return errs
}

func (client *daggerClient) getMainClientCaller() (bksession.Caller, error) {
	return client.getClientCaller(client.daggerSession.mainClientCallerID)
}

func (sess *daggerSession) LoadOrStoreTelemetrySeenKey(key string) bool {
	_, seen := sess.seenKeys.LoadOrStore(key, struct{}{})
	return seen
}

func (sess *daggerSession) StoreTelemetrySeenKey(key string) {
	sess.seenKeys.Store(key, struct{}{})
}

func (sess *daggerSession) FlushTelemetry(ctx context.Context) error {
	eg := new(errgroup.Group)
	sess.clientMu.Lock()
	for _, client := range sess.clients {
		eg.Go(func() error {
			return client.FlushTelemetry(ctx)
		})
	}
	sess.clientMu.Unlock()
	return eg.Wait()
}

// requires that sess.stateMu is held
func (srv *Server) initializeDaggerSession(
	clientMetadata *engine.ClientMetadata,
	sess *daggerSession,
	failureCleanups *cleanups.Cleanups,
) error {
	slog.Info("initializing new session", "session", clientMetadata.SessionID)
	defer slog.Debug("initialized new session", "session", clientMetadata.SessionID)

	sess.sessionID = clientMetadata.SessionID
	sess.mainClientCallerID = clientMetadata.ClientID
	sess.clients = map[string]*daggerClient{}
	sess.endpoints = map[string]http.Handler{}
	sess.closingCtx, sess.cancelClosing = context.WithCancelCause(context.Background())
	sess.shutdownCh = make(chan struct{})
	sess.services = core.NewServices()
	sess.authProvider = auth.NewRegistryAuthProvider()
	sess.resolver = serverresolver.New(serverresolver.Opts{
		Hosts: srv.registryHosts,
		Auth: serverresolver.NewSessionAuthSource(
			sess.authProvider,
			func(ctx context.Context) (*grpc.ClientConn, error) {
				return srv.sessionMainClientConn(ctx, sess)
			},
		),
		ContentStore: srv.contentStore,
		LeaseManager: srv.leaseManager,
	})
	failureCleanups.Add("close session resolver", sess.resolver.Close)
	sess.containers = map[bkgw.Container]struct{}{}
	sess.dagqlCond = sync.NewCond(&sess.dagqlMu)
	sess.telemetryPubSub = srv.telemetryPubSub
	sess.interactive = clientMetadata.Interactive
	sess.interactiveCommand = clientMetadata.InteractiveCommand
	sess.allowedLLMModules = clientMetadata.AllowedLLMModules

	sess.analytics = analytics.New(analytics.Config{
		DoNotTrack: clientMetadata.DoNotTrack || analytics.DoNotTrack(),
		Labels: enginetel.NewLabels(clientMetadata.Labels, nil, nil).
			WithEngineLabel(srv.engineName).
			WithServerLabels(
				engine.Version,
				runtime.GOOS,
				runtime.GOARCH,
				false,
			),
	})
	failureCleanups.Add("close session analytics", sess.analytics.Close)

	sess.state = sessionStateInitialized
	return nil
}

var errSessionClosing = errors.New("session is closing")

func (sess *daggerSession) beginClosing() {
	sess.closeClosingOnce.Do(func() {
		if sess.cancelClosing != nil {
			sess.cancelClosing(errSessionClosing)
		}
	})
}

func (sess *daggerSession) withClosingCancel(ctx context.Context) context.Context {
	ctx, cancel := context.WithCancelCause(ctx)
	go func() {
		select {
		case <-sess.closingCtx.Done():
			cancel(context.Cause(sess.closingCtx))
		case <-ctx.Done():
		}
	}()
	return ctx
}

// requires that sess.stateMu is held
func (srv *Server) removeDaggerSession(ctx context.Context, sess *daggerSession) error {
	slog := slog.With("session", sess.sessionID)

	slog.Info("removing session; stopping client services and flushing")
	defer slog.Debug("session removed")

	// check if the local cache needs pruning after session is removed, prune if so
	defer func() {
		if srv.isShuttingDown() {
			return
		}
		time.AfterFunc(time.Second, srv.throttledGC)
	}()

	srv.daggerSessionsMu.Lock()
	delete(srv.daggerSessions, sess.sessionID)
	srv.daggerSessionsMu.Unlock()

	sess.state = sessionStateRemoved
	sess.beginClosing()

	var errs error

	// in theory none of this should block very long, but add a safeguard just in case
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()

	if err := sess.services.StopSessionServices(ctx, sess.sessionID); err != nil {
		slog.Warn("error stopping services", "error", err)
		errs = errors.Join(errs, fmt.Errorf("stop client services: %w", err))
	}

	slog.Debug("stopped services")

	if sess.resolver != nil {
		errs = errors.Join(errs, sess.resolver.Close())
		sess.resolver = nil
	}

	// release containers + buildkit solver/session state in parallel

	var releaseGroup errgroup.Group
	sess.containersMu.Lock()
	defer sess.containersMu.Unlock()
	for ctr := range sess.containers {
		if ctr != nil {
			releaseGroup.Go(func() error {
				return ctr.Release(ctx)
			})
		}
	}

	for _, client := range sess.clients {
		releaseGroup.Go(func() error {
			var errs error

			// Flush all telemetry.
			errs = errors.Join(errs, client.ShutdownTelemetry(ctx))

			// Close client DB; subscribers may re-open as needed with client.TelemetryDB()
			errs = errors.Join(errs, client.keepAliveTelemetryDB.Close())

			return errs
		})
	}
	errs = errors.Join(errs, releaseGroup.Wait())

	// cleanup analytics and telemetry
	errs = errors.Join(errs, sess.analytics.Close())

	sess.dagqlMu.Lock()
	sess.dagqlClosing = true
	for sess.dagqlInFlight > 0 {
		sess.dagqlCond.Wait()
	}
	sess.dagqlMu.Unlock()

	beforeDagqlEntries := srv.engineCache.Size()
	beforeDagqlStats := srv.engineCache.EntryStats()
	if err := srv.engineCache.ReleaseSession(ctx, sess.sessionID); err != nil {
		slog.Error("error releasing dagql cache", "error", err)
		errs = errors.Join(errs, fmt.Errorf("release dagql cache: %w", err))
	}
	afterDagqlEntries := srv.engineCache.Size()
	afterDagqlStats := srv.engineCache.EntryStats()
	if afterDagqlEntries != beforeDagqlEntries {
		slog.Debug(
			"released dagql cache refs for session",
			"beforeEntries", beforeDagqlEntries,
			"afterEntries", afterDagqlEntries,
			"beforeRetainedCalls", beforeDagqlStats.RetainedCalls,
			"afterRetainedCalls", afterDagqlStats.RetainedCalls,
		)
	} else {
		slog.Debug(
			"session dagql cache release did not change base cache size",
			"entries", afterDagqlEntries,
			"retainedCalls", afterDagqlStats.RetainedCalls,
		)
	}

	// ensure this chan is closed even if the client never explicitly called the /shutdown endpoint
	sess.closeShutdownOnce.Do(func() {
		close(sess.shutdownCh)
	})

	return errs
}

type ClientInitOpts struct {
	*engine.ClientMetadata

	// If this is a nested client, the client ID of the caller that created it
	CallerClientID string

	// If the client is running from a function in a module, this is that module.
	ModuleContext dagql.ObjectResult[*core.Module]

	// If the client is running from a function in a module, this is that function call.
	FunctionCall *core.FunctionCall

	// If the client is executing in an Env context, this is that Env.
	EnvContext dagql.ObjectResult[*core.Env]
}

// requires that client.stateMu is held
func (srv *Server) initializeDaggerClient(
	ctx context.Context,
	client *daggerClient,
	opts *ClientInitOpts,
) error {
	slog := slog.With(
		"isMainClient", client.clientID == client.daggerSession.mainClientCallerID,
		"sessionID", client.daggerSession.sessionID,
		"clientID", client.clientID,
		"mainClientID", client.daggerSession.mainClientCallerID,
	)
	slog.Info("initializing new client")
	var callerG singleflight.Group[string, bksession.Caller]
	getClientCaller := func(id string, noWait bool) (bksession.Caller, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		caller, _, err := callerG.Do(ctx, id, func(ctx context.Context) (bksession.Caller, error) {
			return srv.bkSessionManager.Get(ctx, id, noWait)
		})
		return caller, err
	}
	client.getClientCaller = func(id string) (bksession.Caller, error) {
		return client.resolveClientCaller(id, getClientCaller)
	}

	var err error
	client.dialer = &net.Dialer{
		Resolver: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				if len(srv.dns.Nameservers) == 0 {
					return nil, errors.New("no nameservers configured")
				}

				var errs []error
				for _, ns := range srv.dns.Nameservers {
					conn, err := client.dialer.DialContext(ctx, network, net.JoinHostPort(ns, "53"))
					if err != nil {
						errs = append(errs, err)
						continue
					}

					return conn, nil
				}

				return nil, errors.Join(errs...)
			},
		},
	}

	engineUtilOpts := *srv.engineUtilOpts
	engineUtilOpts.SessionManager = srv.bkSessionManager
	engineUtilOpts.Dialer = client.dialer
	engineUtilOpts.GetClientCaller = client.getClientCaller
	engineUtilOpts.GetMainClientCaller = client.getMainClientCaller
	engineUtilOpts.GetRegistryResolver = srv.RegistryResolver
	engineUtilOpts.Interactive = client.daggerSession.interactive
	engineUtilOpts.InteractiveCommand = client.daggerSession.interactiveCommand
	client.engineUtilClient, err = engineutil.NewClient(ctx, &engineUtilOpts)
	if err != nil {
		return fmt.Errorf("failed to create engine client: %w", err)
	}

	client.fnCall = opts.FunctionCall
	client.env = opts.EnvContext

	// setup the graphql server + module/function state for the client
	client.dagqlRoot = core.NewRoot(srv)
	ctx = dagql.ContextWithOperationLeaseProvider(ctx, dagql.OperationLeaseProviderFunc(func(ctx context.Context) (context.Context, func(context.Context) error, error) {
		if leaseID, ok := leases.FromContext(ctx); ok && leaseID != "" {
			return ctx, func(context.Context) error { return nil }, nil
		}
		return leaseutil.WithLease(ctx, srv.leaseManager, leaseutil.MakeTemporary)
	}))
	// make query available via context to all APIs
	ctx = core.ContextWithQuery(ctx, client.dagqlRoot)

	coreSchemaBase, err := srv.getCoreSchemaBase(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize core schema base: %w", err)
	}
	coreView := call.View(engine.BaseVersion(engine.NormalizeVersion(client.clientVersion)))
	client.dag, err = coreSchemaBase.Fork(ctx, client.dagqlRoot, coreView)
	if err != nil {
		return fmt.Errorf("failed to fork core schema base: %w", err)
	}
	coreMod := coreSchemaBase.CoreMod(coreView)
	client.defaultDeps = core.NewSchemaBuilder(client.dagqlRoot, []core.Mod{coreMod})
	client.servedMods = core.NewSchemaBuilder(client.dagqlRoot, []core.Mod{coreMod})

	if opts.ModuleContext.Self() != nil {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return fmt.Errorf("failed to get engine cache for module context: %w", err)
		}

		attached, err := cache.AttachResult(ctx, opts.SessionID, client.dag, opts.ModuleContext)
		if err != nil {
			return fmt.Errorf("attach module context during client init: %w", err)
		}
		modInst, ok := attached.(dagql.ObjectResult[*core.Module])
		if !ok {
			return fmt.Errorf("attach module context during client init: expected %T, got %T", opts.ModuleContext, attached)
		}
		client.mod = modInst

		// this is needed to set the view of the core api as compatible
		// with the module we're currently calling from
		engineVersion := client.mod.Self().Source.Value.Self().EngineVersion
		coreView = call.View(engine.BaseVersion(engine.NormalizeVersion(engineVersion)))
		client.dag.View = coreView
		coreMod = coreSchemaBase.CoreMod(coreView)

		client.defaultDeps = core.NewSchemaBuilder(client.dagqlRoot, []core.Mod{coreMod})
		client.servedMods = client.mod.Self().Deps.WithRoot(client.dagqlRoot)
		if len(client.mod.Self().ObjectDefs) > 0 {
			client.servedMods = client.servedMods.Append(core.NewUserMod(client.mod))
		}
	} else {
		client.pendingWorkspaceLoad = true
		if clientMD := client.clientMetadata; clientMD != nil && len(clientMD.ExtraModules) > 0 {
			client.pendingExtraModules = clientMD.ExtraModules
		}
	}

	// configure OTel providers that export to SQLite
	client.spanExporter = srv.telemetryPubSub.Spans(client)
	tracerOpts := []sdktrace.TracerProviderOption{
		// save to our own client's DB
		sdktrace.WithSpanProcessor(telemetry.NewLiveSpanProcessor(
			client.spanExporter,
		)),
	}

	logs := srv.telemetryPubSub.Logs(client)
	client.logExporter = logs
	loggerOpts := []sdklog.LoggerProviderOption{
		sdklog.WithResource(telemetry.Resource),
		sdklog.WithProcessor(logs),
	}

	const metricReaderInterval = 5 * time.Second

	client.metricExporter = srv.telemetryPubSub.Metrics(client)
	meterOpts := []sdkmetric.Option{
		sdkmetric.WithResource(telemetry.Resource),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
			client.metricExporter,
			sdkmetric.WithInterval(metricReaderInterval),
		)),
	}

	// export to parent client DBs too
	for _, parent := range client.parents {
		tracerOpts = append(tracerOpts, sdktrace.WithSpanProcessor(
			telemetry.NewLiveSpanProcessor(
				srv.telemetryPubSub.Spans(parent),
			),
		))
		loggerOpts = append(loggerOpts, sdklog.WithProcessor(
			clientLogs{client: parent},
		))
		meterOpts = append(meterOpts, sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(
				srv.telemetryPubSub.Metrics(parent),
				sdkmetric.WithInterval(metricReaderInterval),
			),
		))
	}
	client.tracerProvider = sdktrace.NewTracerProvider(tracerOpts...)
	client.loggerProvider = sdklog.NewLoggerProvider(loggerOpts...)
	client.meterProvider = sdkmetric.NewMeterProvider(meterOpts...)

	client.state = clientStateInitialized
	return nil
}

func (client *daggerClient) resolveClientCaller(
	id string,
	getClientCaller func(string, bool) (bksession.Caller, error),
) (bksession.Caller, error) {
	if id == client.clientID && len(client.parents) > 0 {
		// Synthetic nested clients (e.g. builtin dang evaluation) do not
		// establish their own session attachables. When host-backed services
		// such as git config are requested through the current client ID, fall
		// back to the immediate parent client chain.
		caller, err := getClientCaller(id, true)
		if err != nil || caller != nil {
			return caller, err
		}

		parent := client.parents[len(client.parents)-1]
		return parent.getClientCaller(parent.clientID)
	}

	return getClientCaller(id, false)
}

func (srv *Server) clientFromContext(ctx context.Context) (*daggerClient, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata for session call: %w", err)
	}
	client, err := srv.clientFromIDs(clientMetadata.SessionID, clientMetadata.ClientID)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (srv *Server) clientFromIDs(sessID, clientID string) (*daggerClient, error) {
	if sessID == "" {
		return nil, fmt.Errorf("missing session ID")
	}
	if clientID == "" {
		return nil, fmt.Errorf("missing client ID")
	}
	srv.daggerSessionsMu.RLock()
	defer srv.daggerSessionsMu.RUnlock()
	sess, ok := srv.daggerSessions[sessID]
	if !ok {
		// This error can happen due to per-LLB-vertex deduplication in the buildkit solver,
		// where for instance the first client cancels and closes its session while others
		// are waiting on the result. In this case its safe to retry the operation again with
		// the still connected client metadata.
		err := flightcontrol.RetryableError{Err: fmt.Errorf("session %q not found", sessID)}
		return nil, err
	}

	sess.clientMu.RLock()
	defer sess.clientMu.RUnlock()
	client, ok := sess.clients[clientID]
	if !ok {
		return nil, fmt.Errorf("client %q not found", clientID)
	}

	return client, nil
}

// initialize session+client if needed, return:
// * the initialized client
// * a cleanup func to run when the call is done
func (srv *Server) getOrInitClient(
	ctx context.Context,
	opts *ClientInitOpts,
) (_ *daggerClient, _ func() error, rerr error) {
	if srv.isShuttingDown() {
		return nil, nil, errServerShuttingDown
	}

	sessionID := opts.SessionID
	if sessionID == "" {
		return nil, nil, fmt.Errorf("session ID is required")
	}
	clientID := opts.ClientID
	if clientID == "" {
		return nil, nil, fmt.Errorf("client ID is required")
	}
	token := opts.ClientSecretToken
	if token == "" {
		return nil, nil, fmt.Errorf("client secret token is required")
	}

	// cleanup to do if this method fails
	failureCleanups := &cleanups.Cleanups{}
	defer func() {
		if rerr != nil {
			rerr = errors.Join(rerr, failureCleanups.Run())
		}
	}()

	// get or initialize the session as a whole

	srv.daggerSessionsMu.Lock()
	if srv.isShuttingDown() {
		srv.daggerSessionsMu.Unlock()
		return nil, nil, errServerShuttingDown
	}
	sess, sessionExists := srv.daggerSessions[sessionID]
	if !sessionExists {
		sess = &daggerSession{
			state: sessionStateUninitialized,
		}
		srv.daggerSessions[sessionID] = sess

		failureCleanups.Add("delete session ID", func() error {
			srv.daggerSessionsMu.Lock()
			delete(srv.daggerSessions, sessionID)
			srv.daggerSessionsMu.Unlock()
			return nil
		})
	}
	srv.daggerSessionsMu.Unlock()

	sess.stateMu.Lock()
	defer sess.stateMu.Unlock()
	switch sess.state {
	case sessionStateUninitialized:
		if err := srv.initializeDaggerSession(opts.ClientMetadata, sess, failureCleanups); err != nil {
			return nil, nil, fmt.Errorf("initialize session: %w", err)
		}
	case sessionStateInitialized:
		// nothing to do
	case sessionStateRemoved:
		return nil, nil, fmt.Errorf("session %q removed", sess.sessionID)
	}

	// get or initialize the client itself

	sess.clientMu.Lock()
	client, clientExists := sess.clients[clientID]
	if !clientExists {
		client = &daggerClient{
			state:          clientStateUninitialized,
			daggerSession:  sess,
			clientID:       clientID,
			clientVersion:  opts.ClientVersion,
			secretToken:    token,
			shutdownCh:     make(chan struct{}),
			clientMetadata: opts.ClientMetadata,
		}
		sess.clients[clientID] = client

		// initialize SQLite DB early so we can subscribe to it immediately
		if db, err := srv.clientDBs.Open(ctx, client.clientID); err != nil {
			slog.Warn("failed to open client DB; continuing without keepalive",
				"sessionID", sessionID,
				"clientID", client.clientID,
				"error", err,
			)
		} else {
			client.keepAliveTelemetryDB = db
			failureCleanups.Add("close client telemetry DB", func() error {
				return db.Close()
			})
		}

		parent, parentExists := sess.clients[opts.CallerClientID]
		if parentExists {
			client.parents = slices.Clone(parent.parents)
			client.parents = append(client.parents, parent)
		}

		failureCleanups.Add("delete client ID", func() error {
			sess.clientMu.Lock()
			delete(sess.clients, clientID)
			sess.clientMu.Unlock()
			return nil
		})
	}
	sess.clientMu.Unlock()

	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	switch client.state {
	case clientStateUninitialized:
		if err := srv.initializeDaggerClient(ctx, client, opts); err != nil {
			return nil, nil, fmt.Errorf("initialize client: %w", err)
		}
	case clientStateInitialized:
		// verify token matches existing client
		if token != client.secretToken {
			return nil, nil, fmt.Errorf("client %q already exists with different secret token", clientID)
		}

		// for nested clients running the dagger cli, the session attachable
		// connection may not have all of the client metadata yet, so we
		// fill in some missing fields here that may be set later by the cli
		if client.clientMetadata.AllowedLLMModules == nil {
			client.clientMetadata.AllowedLLMModules = opts.AllowedLLMModules
		}
		if opts.LoadWorkspaceModules {
			client.clientMetadata.LoadWorkspaceModules = true
		}
		if client.clientMetadata.Workspace == nil && !client.workspaceLoaded {
			if workspaceRef, ok := workspaceRefFromClientMetadata(opts.ClientMetadata); ok {
				ref := workspaceRef
				client.clientMetadata.Workspace = &ref
			}
		}
		// ExtraModules may arrive on a later request (e.g. /init) after the
		// session attachable request already created the client without them.
		if len(opts.ExtraModules) > 0 && len(client.pendingExtraModules) == 0 && !client.modulesLoaded {
			client.clientMetadata.ExtraModules = opts.ExtraModules
			client.pendingExtraModules = opts.ExtraModules
		}
	}

	// increment the number of active connections from this client
	client.activeCount++

	return client, func() error {
		client.stateMu.Lock()
		defer client.stateMu.Unlock()
		client.activeCount--

		if client.activeCount > 0 {
			return nil
		}

		slog := slog.With(
			"sessionID", sess.sessionID,
			"clientID", client.clientID,
		)
		slog.Info("all client connections closed")

		// if the main client caller has no more active calls, cleanup the whole session
		if clientID != sess.mainClientCallerID {
			return nil
		}

		sess.stateMu.Lock()
		defer sess.stateMu.Unlock()
		switch sess.state {
		case sessionStateInitialized:
			return srv.removeDaggerSession(ctx, sess)
		default:
			// this should never happen unless there's a bug
			slog.Error("session state being removed not in initialized state",
				"state", sess.state,
			)
			return nil
		}
	}, nil
}

// ServeHTTP serves clients directly hitting the engine API (i.e. main client callers, not nested execs like module functions)
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	clientMetadata, err := engine.ClientMetadataFromHTTPHeaders(r.Header)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get client metadata: %v", err), http.StatusInternalServerError)
		return
	}

	if !engine.CheckVersionCompatibility(engine.NormalizeVersion(clientMetadata.ClientVersion), engine.MinimumClientVersion) {
		http.Error(w, fmt.Sprintf("incompatible client version %s", engine.NormalizeVersion(clientMetadata.ClientVersion)), http.StatusInternalServerError)
		return
	}

	httpHandlerFunc(srv.serveHTTPToClient, &ClientInitOpts{
		ClientMetadata: clientMetadata,
	}).ServeHTTP(w, r)
}

// ServeHTTPToNestedClient serves nested clients, including module function calls.
func (srv *Server) ServeHTTPToNestedClient(
	w http.ResponseWriter,
	r *http.Request,
	nestedClientMetadata *engine.ClientMetadata,
	callerClientID string,
	moduleCtx dagql.AnyObjectResult,
	functionCall dagql.Typed,
	envCtx dagql.AnyObjectResult,
) {
	if nestedClientMetadata == nil {
		http.Error(w, "nested client metadata is nil", http.StatusInternalServerError)
		return
	}
	clientMetadata := *nestedClientMetadata
	clientMetadata.AllowedLLMModules = slices.Clone(nestedClientMetadata.AllowedLLMModules)
	if clientMetadata.ClientVersion == "" {
		clientMetadata.ClientVersion = engine.Version
	}
	clientMetadata.Labels = map[string]string{}

	var extraModules []engine.ExtraModule
	var loadWorkspaceModules bool
	var eagerRuntime bool
	var workspaceRef *string
	if md, _ := engine.ClientMetadataFromHTTPHeaders(r.Header); md != nil {
		clientMetadata.ClientVersion = md.ClientVersion
		extraModules = md.ExtraModules
		loadWorkspaceModules = md.LoadWorkspaceModules
		eagerRuntime = md.EagerRuntime
		if declaredWorkspace, ok := workspaceRefFromClientMetadata(md); ok {
			ref := declaredWorkspace
			workspaceRef = &ref
		}
		if md.LockMode != "" {
			clientMetadata.LockMode = md.LockMode
		}
	}

	var moduleContext dagql.ObjectResult[*core.Module]
	if moduleCtx != nil {
		typed, ok := moduleCtx.(dagql.ObjectResult[*core.Module])
		if !ok {
			http.Error(w, fmt.Sprintf("nested client module context is %T, not Module", moduleCtx), http.StatusInternalServerError)
			return
		}
		if typed.Self() != nil {
			moduleContext = typed
		}
	}

	var fnCall *core.FunctionCall
	if functionCall != nil {
		typed, ok := functionCall.(*core.FunctionCall)
		if !ok {
			http.Error(w, fmt.Sprintf("nested client function call is %T, not FunctionCall", functionCall), http.StatusInternalServerError)
			return
		}
		fnCall = typed
	}

	var envContext dagql.ObjectResult[*core.Env]
	if envCtx != nil {
		typed, ok := envCtx.(dagql.ObjectResult[*core.Env])
		if !ok {
			http.Error(w, fmt.Sprintf("nested client env context is %T, not Env", envCtx), http.StatusInternalServerError)
			return
		}
		if typed.Self() != nil {
			envContext = typed
		}
	}

	clientMetadata.ExtraModules = extraModules
	clientMetadata.LoadWorkspaceModules = loadWorkspaceModules
	clientMetadata.EagerRuntime = eagerRuntime
	clientMetadata.Workspace = workspaceRef

	httpHandlerFunc(srv.serveHTTPToClient, &ClientInitOpts{
		ClientMetadata: &clientMetadata,
		CallerClientID: callerClientID,
		ModuleContext:  moduleContext,
		FunctionCall:   fnCall,
		EnvContext:     envContext,
	}).ServeHTTP(w, r)
}

const InstrumentationLibrary = "dagger.io/engine.server"

func (srv *Server) serveHTTPToClient(w http.ResponseWriter, r *http.Request, opts *ClientInitOpts) (rerr error) {
	if srv.isShuttingDown() {
		switch r.URL.Path {
		case engine.QueryEndpoint:
			return gqlErr(errServerShuttingDown, http.StatusServiceUnavailable)
		default:
			return httpErr(errServerShuttingDown, http.StatusServiceUnavailable)
		}
	}

	ctx := srv.withShutdownCancel(r.Context())

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(fmt.Errorf("http request done for client %q", opts.ClientID))

	clientMetadata := opts.ClientMetadata
	ctx = engine.ContextWithClientMetadata(ctx, clientMetadata)

	// propagate span context and baggage from the client
	ctx = telemetry.Propagator.Extract(ctx, propagation.HeaderCarrier(r.Header))

	// Check if repeated telemetry is requested via baggage
	baggage := baggage.FromContext(ctx)
	if member := baggage.Member("repeat-telemetry"); member.Value() != "" {
		ctx = dagql.WithRepeatedTelemetry(ctx)
	}

	ctx = bklog.WithLogger(ctx, bklog.G(ctx).
		WithField("trace", trace.SpanContextFromContext(ctx).TraceID().String()).
		WithField("span", trace.SpanContextFromContext(ctx).SpanID().String()).
		WithField("client_id", clientMetadata.ClientID).
		WithField("client_hostname", clientMetadata.ClientHostname).
		WithField("session_id", clientMetadata.SessionID))
	ctx = slog.WithLogger(ctx, slog.FromContext(ctx).With(
		"client_id", clientMetadata.ClientID,
		"client_hostname", clientMetadata.ClientHostname,
		"session_id", clientMetadata.SessionID,
		"trace", trace.SpanContextFromContext(ctx).TraceID().String(),
		"span", trace.SpanContextFromContext(ctx).SpanID().String(),
	))
	ctx = dagql.ContextWithCache(ctx, srv.engineCache)

	// Debug https://github.com/dagger/dagger/issues/7592 by logging method and some headers, which
	// are checked by gqlgen's handler
	bklog.G(ctx).WithFields(logrus.Fields{
		"path":          r.URL.Path,
		"method":        r.Method,
		"upgradeHeader": r.Header.Get("Upgrade"),
		"contentType":   r.Header.Get("Content-Type"),
		"trace":         trace.SpanContextFromContext(ctx).TraceID().String(),
		"span":          trace.SpanContextFromContext(ctx).SpanID().String(),
	}).Debug("handling http request")

	mux := http.NewServeMux()
	switch r.URL.Path {
	case "/v1/traces", "/v1/logs", "/v1/metrics":
		// Just get the client if it exists, don't init it.
		client, err := srv.clientFromIDs(clientMetadata.SessionID, clientMetadata.ClientID)
		if err != nil {
			return fmt.Errorf("get client: %w", err)
		}
		mux.HandleFunc("GET /v1/traces", httpHandlerFunc(srv.telemetryPubSub.TracesSubscribeHandler, client))
		mux.HandleFunc("GET /v1/logs", httpHandlerFunc(srv.telemetryPubSub.LogsSubscribeHandler, client))
		mux.HandleFunc("GET /v1/metrics", httpHandlerFunc(srv.telemetryPubSub.MetricsSubscribeHandler, client))
	default:
		client, cleanup, err := srv.getOrInitClient(ctx, opts)
		if err != nil {
			err = fmt.Errorf("get or init client: %w", err)
			switch r.URL.Path {
			case engine.QueryEndpoint:
				err = gqlErr(err, http.StatusInternalServerError)
			default:
				err = httpErr(err, http.StatusInternalServerError)
			}
			return err
		}
		defer func() {
			err := cleanup()
			if err != nil {
				bklog.G(ctx).WithError(err).Error("client serve cleanup failed")
				rerr = errors.Join(rerr, err)
			}
		}()

		sess := client.daggerSession
		ctx = analytics.WithContext(ctx, sess.analytics)
		r = r.WithContext(ctx)

		mux.Handle(engine.SessionAttachablesEndpoint, httpHandlerFunc(srv.serveSessionAttachables, client))
		mux.Handle(engine.QueryEndpoint, httpHandlerFunc(srv.serveQuery, client))
		mux.Handle(engine.InitEndpoint, httpHandlerFunc(srv.serveInit, client))
		mux.Handle(engine.ShutdownEndpoint, httpHandlerFunc(srv.serveShutdown, client))
		sess.endpointMu.RLock()
		for path, handler := range sess.endpoints {
			mux.Handle(path, handler)
		}
		sess.endpointMu.RUnlock()
	}

	mux.ServeHTTP(w, r)
	return nil
}

func (srv *Server) serveSessionAttachables(w http.ResponseWriter, r *http.Request, client *daggerClient) (rerr error) {
	ctx := r.Context()
	bklog.G(ctx).Debugf("session manager handling conn %s", client.clientID)
	defer func() {
		bklog.G(ctx).WithError(rerr).Debugf("session manager handle conn done %s", client.clientID)
		slog.ExtraDebug("session manager handle conn done",
			"err", rerr,
			"ctxErr", ctx.Err(),
			"clientID", client.clientID,
		)
	}()

	// verify this isn't overwriting an existing active session
	existingCaller, err := srv.bkSessionManager.Get(ctx, client.clientID, true)
	if err == nil && existingCaller != nil {
		err := fmt.Errorf("buildkit session %q already exists", client.clientID)
		return httpErr(err, http.StatusBadRequest)
	}

	// hijack the connection so we can use it for our gRPC client
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return errors.New("handler does not support hijack")
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		return fmt.Errorf("failed to hijack connection: %w", err)
	}
	defer func() {
		if rerr != nil {
			conn.Close()
		}
	}()
	if err := conn.SetDeadline(time.Time{}); err != nil {
		panic(fmt.Errorf("failed to clear deadline: %w", err))
	}

	// confirm to the client that everything went swimmingly and we're ready to become a gRPC client
	resp := &http.Response{
		StatusCode: http.StatusSwitchingProtocols,
		Header:     http.Header{},
	}
	resp.Header.Set("Connection", "Upgrade")
	resp.Header.Set("Upgrade", "h2c")
	if err := resp.Write(conn); err != nil {
		panic(fmt.Errorf("failed to write response: %w", err))
	}

	// The client confirms it has fully read the response and is ready to serve gRPC by sending
	// a single byte ack. This prevents race conditions where we start trying to connect gRPC clients
	// before the response has been fully read, which can mix http and gRPC traffic.
	ack := make([]byte, 1)
	if _, err := conn.Read(ack); err != nil {
		panic(fmt.Errorf("failed to read ack: %w", err))
	}

	ctx = client.daggerSession.withClosingCancel(ctx)

	// Disable collecting otel metrics on these grpc connections for now. We don't use them and
	// they add noticeable memory allocation overhead, especially for heavy filesync use cases.
	ctx = trace.ContextWithSpan(ctx, trace.SpanFromContext(nil)) //nolint:staticcheck // we have to provide a nil context...

	err = srv.bkSessionManager.HandleConn(ctx, conn, map[string][]string{
		engine.SessionIDMetaKey:         {client.clientID},
		engine.SessionNameMetaKey:       {client.clientID},
		engine.SessionSharedKeyMetaKey:  {""},
		engine.SessionMethodNameMetaKey: r.Header.Values(engine.SessionMethodNameMetaKey),
	})
	if err != nil {
		panic(fmt.Errorf("handleConn: %w", err))
	}
	return nil
}

func (srv *Server) serveQuery(w http.ResponseWriter, r *http.Request, client *daggerClient) (rerr error) {
	sess := client.daggerSession
	sess.dagqlMu.Lock()
	if sess.dagqlClosing {
		sess.dagqlMu.Unlock()
		return gqlErr(errSessionClosing, http.StatusServiceUnavailable)
	}
	sess.dagqlInFlight++
	sess.dagqlMu.Unlock()
	defer func() {
		sess.dagqlMu.Lock()
		sess.dagqlInFlight--
		if sess.dagqlInFlight == 0 {
			sess.dagqlCond.Broadcast()
		}
		sess.dagqlMu.Unlock()
	}()

	ctx := sess.withClosingCancel(r.Context())

	// turn panics into graphql errors — must be set up before any code that
	// could panic (including ensureExtraModulesLoaded and schema loading).
	defer func() {
		if v := recover(); v != nil {
			bklog.G(ctx).Errorf("panic serving schema: %v %s", v, string(debug.Stack()))
			switch v := v.(type) {
			case error:
				rerr = gqlErr(v, http.StatusInternalServerError)
			case string:
				rerr = gqlErr(errors.New(v), http.StatusInternalServerError)
			default:
				rerr = gqlErr(errors.New("internal server error"), http.StatusInternalServerError)
			}
		}
	}()

	// only record telemetry if the request is traced, otherwise
	// we end up with orphaned spans in their own separate traces from tests etc.
	if trace.SpanContextFromContext(ctx).IsValid() {
		// create a span to record telemetry into the client's DB
		//
		// downstream components must use otel.SpanFromContext(ctx).TracerProvider()
		clientTracer := client.tracerProvider.Tracer(InstrumentationLibrary)
		var span trace.Span
		attrs := []attribute.KeyValue{
			attribute.Bool(telemetry.UIPassthroughAttr, true),
		}
		if engineID := client.clientMetadata.CloudScaleOutEngineID; engineID != "" {
			attrs = append(attrs, attribute.String(telemetry.EngineIDAttr, engineID))
		}
		ctx, span = clientTracer.Start(ctx,
			fmt.Sprintf("%s %s", r.Method, r.URL.Path),
			trace.WithAttributes(attrs...),
		)
		defer telemetry.EndWithCause(span, &rerr)
	}

	// install a logger+meter provider that records to the client's DB
	ctx = telemetry.WithLoggerProvider(ctx, client.loggerProvider)
	ctx = telemetry.WithMeterProvider(ctx, client.meterProvider)

	ctx = dagql.ContextWithOperationLeaseProvider(ctx, dagql.OperationLeaseProviderFunc(func(ctx context.Context) (context.Context, func(context.Context) error, error) {
		if leaseID, ok := leases.FromContext(ctx); ok && leaseID != "" {
			return ctx, func(context.Context) error { return nil }, nil
		}
		return leaseutil.WithLease(ctx, srv.leaseManager, leaseutil.MakeTemporary)
	}))

	// make query available via context to all APIs
	ctx = core.ContextWithQuery(ctx, client.dagqlRoot)

	r = r.WithContext(ctx)

	// Load workspace modules and extra modules (e.g. from -m flag). These are
	// deferred from initializeDaggerClient because they need the client's
	// buildkit session, which only becomes available after the session
	// attachables handshake completes (after init locks are released).
	if err := srv.ensureWorkspaceLoaded(ctx, client); err != nil {
		return gqlErr(fmt.Errorf("loading workspace: %w", err), http.StatusInternalServerError)
	}
	if err := srv.ensureModulesLoaded(ctx, client); err != nil {
		return gqlErr(fmt.Errorf("loading modules: %w", err), http.StatusInternalServerError)
	}

	// get the schema we're gonna serve to this client based on which modules they have loaded, if any
	schema, err := client.servedMods.Schema(ctx)
	if err != nil {
		return gqlErr(fmt.Errorf("failed to get schema: %w", err), http.StatusBadRequest)
	}

	gqlSrv := dagql.NewDefaultHandler(schema)
	// NB: break glass when needed:
	// gqlSrv.AroundResponses(func(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	// 	res := next(ctx)
	// 	pl, err := json.Marshal(res)
	// 	slog.Debug("graphql response", "response", string(pl), "error", err)
	// 	return res
	// })

	gqlSrv.ServeHTTP(w, r)
	return nil
}

func (srv *Server) serveInit(w http.ResponseWriter, _ *http.Request, client *daggerClient) (rerr error) {
	sess := client.daggerSession
	slog := slog.With(
		"isMainClient", client.clientID == sess.mainClientCallerID,
		"sessionID", sess.sessionID,
		"clientID", client.clientID,
		"mainClientID", sess.mainClientCallerID)

	slog.Trace("initialized client")

	// nothing to actually do, client was passed in
	w.WriteHeader(http.StatusNoContent)

	return nil
}

func (srv *Server) serveShutdown(w http.ResponseWriter, r *http.Request, client *daggerClient) (rerr error) {
	ctx := r.Context()
	var shutdownErr error

	sess := client.daggerSession
	slog := slog.With(
		"isMainClient", client.clientID == sess.mainClientCallerID,
		"sessionID", sess.sessionID,
		"clientID", client.clientID,
		"mainClientID", sess.mainClientCallerID)

	slog.Info("client shutdown")
	defer slog.Debug("client shutdown done")

	if client.clientID == sess.mainClientCallerID {
		slog.Info("main client is shutting down")
		flushCtx := context.WithoutCancel(ctx)
		if err := srv.flushWorkspaceLocks(flushCtx, client); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("flush workspace locks: %w", err))
			slog.Error("failed to flush workspace locks", "error", err)
		}

		// this must be done after lockfile flushing (since lockfiles make use of attachables to write data to host)
		sess.beginClosing()

		// Stop services, since the main client is going away, and we
		// want the client to see them stop.
		sess.services.StopSessionServices(ctx, sess.sessionID)

		defer func() {
			// Signal shutdown at the very end, _after_ flushing telemetry/etc.,
			// so we can respect the shutdownCh to short-circuit any telemetry
			// subscribers that appeared _while_ shutting down.
			sess.closeShutdownOnce.Do(func() {
				close(sess.shutdownCh)
			})
		}()
	} else {
		flushCtx := context.WithoutCancel(ctx)
		if err := srv.flushWorkspaceLocks(flushCtx, client); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("flush workspace locks: %w", err))
			slog.Error("failed to flush workspace locks", "error", err)
		}
	}

	// Flush telemetry across the entire session so that any child clients will
	// save telemetry into their parent's DB, including to this client.
	slog.ExtraDebug("flushing session telemetry")
	if err := sess.FlushTelemetry(ctx); err != nil {
		slog.Error("failed to flush telemetry", "error", err)
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("flush session telemetry: %w", err))
	}

	client.closeShutdownOnce.Do(func() {
		close(client.shutdownCh)
	})

	return shutdownErr
}

// Stitch in the given module to the list being served to the current client.
// When includeDependencies is true, dependency modules and toolchains are
// also served with their constructors on the Query root.
// When entrypoint is true, the module's main-object methods are promoted
// onto the Query root.
func (srv *Server) ServeModule(ctx context.Context, mod dagql.ObjectResult[*core.Module], includeDependencies bool, entrypoint bool) error {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return err
	}

	client.stateMu.Lock()
	defer client.stateMu.Unlock()

	if err := srv.serveModule(client, core.NewUserMod(mod), core.InstallOpts{Entrypoint: entrypoint}); err != nil {
		return err
	}
	if includeDependencies {
		for _, dep := range mod.Self().Deps.Mods() {
			if err := srv.serveModule(client, dep, core.InstallOpts{}); err != nil {
				return fmt.Errorf("error serving dependency %s: %w", dep.Name(), err)
			}
		}

		// Also serve toolchains so their functions are available in the
		// client schema (e.g. when `dagger shell` `.cd`s into a module).
		if mod.Self().Source.Valid && mod.Self().Source.Value.Self() != nil {
			src := mod.Self().Source.Value
			defaultPathContextSrc := src
			if mod.Self().ContextSource.Valid && mod.Self().ContextSource.Value.Self() != nil {
				defaultPathContextSrc = mod.Self().ContextSource.Value
			}
			for i, tcSrc := range src.Self().Toolchains {
				if tcSrc.Self() == nil {
					continue
				}
				var cfg *modules.ModuleConfigDependency
				if i < len(src.Self().ConfigToolchains) {
					cfg = src.Self().ConfigToolchains[i]
				}
				pending := pendingRelatedModule(defaultPathContextSrc, tcSrc.Self(), cfg, false)
				tcMod, err := srv.resolveModuleSourceAsModule(ctx, client.dag, tcSrc, pending)
				if err != nil {
					return fmt.Errorf("error resolving toolchain module: %w", err)
				}
				if err := srv.serveModule(client, core.NewUserMod(tcMod), core.InstallOpts{}); err != nil {
					return fmt.Errorf("error serving toolchain %s: %w", tcMod.Self().Name(), err)
				}
			}
		}
	}
	return nil
}

// serveModule adds a module to the client's served set with the given install
// policy.
//
// Not threadsafe: client.stateMu must be held when calling.
func (srv *Server) serveModule(client *daggerClient, mod core.Mod, opts core.InstallOpts) error {
	existing, ok := client.servedMods.Lookup(mod.Name())
	if ok {
		if !isSameModuleReference(existing.GetSource(), mod.GetSource()) {
			return fmt.Errorf("module %s (source: %s | pin: %s) already exists with different source %s (pin: %s)",
				mod.Name(), mod.GetSource().AsString(), mod.GetSource().Pin(), existing.GetSource().AsString(), existing.GetSource().Pin(),
			)
		}
	}
	// With handles deduplication and promotion internally.
	client.servedMods = client.servedMods.With(mod, opts)
	return nil
}

// Returns true if the module source a is the same as b or they come from the
// core module.
// Returns false if:
// - AsString() of a and b are different
// - Pin() of a and b are different
func isSameModuleReference(a *core.ModuleSource, b *core.ModuleSource) bool {
	// If one of them is empty, that means they are from code module so they shouldn't
	// be compared.
	if a.AsString() == "" || b.AsString() == "" {
		return true
	}

	// Match by canonical module identity:
	// - local: absolute source-code path (context dir + sourceSubpath)
	// - git: clone ref + sourceSubpath
	// plus pin equality for immutable provenance.
	if canonicalModuleReference(a) != canonicalModuleReference(b) {
		return false
	}
	return a.Pin() == b.Pin()
}
func (srv *Server) CurrentWorkspaceLock(ctx context.Context) (*workspace.Lock, bool, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, false, err
	}

	ws, key, lockPath, ok, err := srv.currentWorkspaceLockBinding(client)
	if err != nil || !ok {
		return nil, ok, err
	}

	sess := client.daggerSession

	sess.lockFileMu.RLock()
	if state, ok := sess.lockFiles[key]; ok && state.loaded {
		cloned, err := state.lock.Clone()
		sess.lockFileMu.RUnlock()
		return cloned, true, err
	}
	sess.lockFileMu.RUnlock()

	sess.lockFileMu.Lock()
	defer sess.lockFileMu.Unlock()

	state, err := srv.loadWorkspaceLockStateLocked(ctx, client, ws, key, lockPath)
	if err != nil {
		return nil, false, err
	}
	cloned, err := state.lock.Clone()
	if err != nil {
		return nil, false, err
	}
	return cloned, true, nil
}

func (srv *Server) SetCurrentWorkspaceLookup(
	ctx context.Context,
	namespace string,
	operation string,
	inputs []any,
	result workspace.LookupResult,
) error {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return err
	}

	ws, key, lockPath, ok, err := srv.currentWorkspaceLockBinding(client)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("workspace lock is not available")
	}

	sess := client.daggerSession
	sess.lockFileMu.Lock()
	defer sess.lockFileMu.Unlock()

	state, err := srv.loadWorkspaceLockStateLocked(ctx, client, ws, key, lockPath)
	if err != nil {
		return err
	}
	if err := state.lock.SetLookup(namespace, operation, inputs, result); err != nil {
		return err
	}
	if err := state.delta.SetLookup(namespace, operation, inputs, result); err != nil {
		return err
	}
	state.dirty = true
	return nil
}

func (srv *Server) currentWorkspaceLockBinding(client *daggerClient) (*core.Workspace, workspaceLockKey, string, bool, error) {
	ws := client.workspace
	if ws == nil || ws.HostPath() == "" {
		return nil, workspaceLockKey{}, "", false, nil
	}
	lockPath, err := workspaceLockPath(ws)
	if err != nil {
		return nil, workspaceLockKey{}, "", false, err
	}
	return ws, workspaceLockKey{
		ownerClientID: ws.ClientID,
		lockPath:      lockPath,
	}, lockPath, true, nil
}

func (srv *Server) loadWorkspaceLockStateLocked(
	ctx context.Context,
	client *daggerClient,
	ws *core.Workspace,
	key workspaceLockKey,
	lockPath string,
) (*workspaceLockState, error) {
	sess := client.daggerSession
	if sess.lockFiles == nil {
		sess.lockFiles = make(map[workspaceLockKey]*workspaceLockState)
	}
	if state, ok := sess.lockFiles[key]; ok && state.loaded {
		return state, nil
	}

	workspaceCtx, bk, err := srv.workspaceOwnerAccess(ctx, sess, ws)
	if err != nil {
		return nil, err
	}
	lock, err := readWorkspaceLockState(workspaceCtx, bk, ws)
	if err != nil {
		return nil, err
	}

	state := &workspaceLockState{
		ws:       ws.Clone(),
		lockPath: lockPath,
		lock:     lock,
		delta:    workspace.NewLock(),
		loaded:   true,
	}
	sess.lockFiles[key] = state
	return state, nil
}

func (srv *Server) workspaceOwnerAccess(
	ctx context.Context,
	sess *daggerSession,
	ws *core.Workspace,
) (context.Context, *engineutil.Client, error) {
	if ws.ClientID == "" {
		return nil, nil, fmt.Errorf("workspace has no client ID")
	}

	ownerClient, err := srv.clientFromIDs(sess.sessionID, ws.ClientID)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace owner client: %w", err)
	}
	if ownerClient.clientMetadata == nil {
		return nil, nil, fmt.Errorf("workspace owner client metadata not initialized")
	}
	if ownerClient.engineUtilClient == nil {
		return nil, nil, fmt.Errorf("workspace owner buildkit client not initialized")
	}

	workspaceCtx := engine.ContextWithClientMetadata(ctx, ownerClient.clientMetadata)
	return workspaceCtx, ownerClient.engineUtilClient, nil
}

func workspaceLockPath(ws *core.Workspace) (string, error) {
	if ws == nil {
		return "", fmt.Errorf("workspace is required")
	}
	if ws.HostPath() == "" {
		return "", fmt.Errorf("workspace has no host path")
	}
	return filepath.Join(ws.HostPath(), ws.Path, workspace.LockDirName, workspace.LockFileName), nil
}

func readWorkspaceLockState(ctx context.Context, bk interface {
	ReadCallerHostFile(ctx context.Context, path string) ([]byte, error)
}, ws *core.Workspace) (*workspace.Lock, error) {
	lockPath, err := workspaceLockPath(ws)
	if err != nil {
		return nil, err
	}

	data, err := bk.ReadCallerHostFile(ctx, lockPath)
	if err != nil {
		if isWorkspaceLockNotFound(err) {
			return workspace.NewLock(), nil
		}
		return nil, fmt.Errorf("reading lock: %w", err)
	}

	lock, err := workspace.ParseLock(data)
	if err != nil {
		return nil, fmt.Errorf("parsing lock: %w", err)
	}
	return lock, nil
}

func isWorkspaceLockNotFound(err error) bool {
	return errors.Is(err, os.ErrNotExist) || status.Code(err) == codes.NotFound
}

func exportWorkspaceLockToHost(ctx context.Context, bk *engineutil.Client, ws *core.Workspace, lock *workspace.Lock) error {
	lockBytes, err := lock.Marshal()
	if err != nil {
		return fmt.Errorf("marshal lock: %w", err)
	}

	lockPath, err := workspaceLockPath(ws)
	if err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp("", "workspace-lock-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(lockBytes); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := bk.LocalFileExport(ctx, tmpFile.Name(), workspace.LockFileName, lockPath, true); err != nil {
		return fmt.Errorf("export lock: %w", err)
	}
	return nil
}

func (srv *Server) flushWorkspaceLocks(ctx context.Context, client *daggerClient) error {
	sess := client.daggerSession

	type pendingWorkspaceLockExport struct {
		ws       *core.Workspace
		lockPath string
		delta    *workspace.Lock
	}

	var pending []pendingWorkspaceLockExport

	sess.lockFileMu.RLock()
	for _, state := range sess.lockFiles {
		if state == nil || !state.loaded || !state.dirty {
			continue
		}
		delta, err := state.delta.Clone()
		if err != nil {
			sess.lockFileMu.RUnlock()
			return fmt.Errorf("clone workspace lock delta for %s: %w", state.lockPath, err)
		}
		if state.ws.ClientID == client.clientID {
			pending = append(pending, pendingWorkspaceLockExport{
				ws:       state.ws.Clone(),
				lockPath: state.lockPath,
				delta:    delta,
			})
		}
	}
	sess.lockFileMu.RUnlock()

	var flushErr error
	for _, export := range pending {
		srv.locker.Lock(export.lockPath)

		workspaceCtx, bk, err := srv.workspaceOwnerAccess(ctx, sess, export.ws)
		if err == nil {
			var latest *workspace.Lock
			latest, err = readWorkspaceLockState(workspaceCtx, bk, export.ws)
			if err == nil {
				err = latest.Merge(export.delta)
			}
			if err == nil {
				err = exportWorkspaceLockToHost(workspaceCtx, bk, export.ws, latest)
			}
		}

		srv.locker.Unlock(export.lockPath)

		if err != nil {
			flushErr = errors.Join(flushErr, fmt.Errorf("flush workspace lock %s: %w", export.lockPath, err))
		}
	}

	return flushErr
}

// If the current client is coming from a function, return the module that function is from
func (srv *Server) CurrentModule(ctx context.Context) (dagql.ObjectResult[*core.Module], error) {
	var zero dagql.ObjectResult[*core.Module]
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return zero, err
	}
	if client.clientID == client.daggerSession.mainClientCallerID {
		return zero, fmt.Errorf("%w: main client caller has no current module", core.ErrNoCurrentModule)
	}
	if client.mod.Self() != nil {
		return client.mod, nil
	}

	return zero, core.ErrNoCurrentModule
}

// If the current client is a module client or a client created by a module function, returns that module.
func (srv *Server) ModuleParent(ctx context.Context) (dagql.ObjectResult[*core.Module], error) {
	var zero dagql.ObjectResult[*core.Module]
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return zero, err
	}
	if client.mod.Self() != nil {
		return client.mod, nil
	}
	for i := len(client.parents) - 1; i >= 0; i-- {
		parent := client.parents[i]
		if parent.mod.Self() != nil {
			return parent.mod, nil
		}
	}
	return zero, core.ErrNoCurrentModule
}

// If the current client is coming from a function, return the function call metadata
func (srv *Server) CurrentFunctionCall(ctx context.Context) (*core.FunctionCall, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if client.clientID == client.daggerSession.mainClientCallerID {
		return nil, fmt.Errorf("%w: main client caller has no current module", core.ErrNoCurrentModule)
	}
	return client.fnCall, nil
}

func (srv *Server) CurrentEnv(ctx context.Context) (dagql.ObjectResult[*core.Env], error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Env]{}, err
	}
	return client.env, nil
}

// Return the modules being served to the current client
func (srv *Server) CurrentServedDeps(ctx context.Context) (*core.SchemaBuilder, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.servedMods, nil
}

// The Client metadata of the main client caller (i.e. the one who created the
// session, typically the CLI invoked by the user)
func (srv *Server) MainClientCallerMetadata(ctx context.Context) (*engine.ClientMetadata, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return srv.SpecificClientMetadata(ctx, client.daggerSession.mainClientCallerID)
}

// The Client metadata of a specific client ID within the same session as the
// current client.
func (srv *Server) SpecificClientMetadata(ctx context.Context, clientID string) (*engine.ClientMetadata, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	clientMD, err := srv.clientFromIDs(client.daggerSession.sessionID, clientID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve session main client: %w", err)
	}
	return clientMD.clientMetadata, nil
}

func (srv *Server) SpecificClientAttachableConn(ctx context.Context, clientID string) (*grpc.ClientConn, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := client.getClientCaller(clientID)
	if err != nil {
		return nil, fmt.Errorf("failed to get session attachable caller for client %q: %w", clientID, err)
	}
	if caller == nil {
		return nil, fmt.Errorf("session attachable caller for client %q was nil", clientID)
	}
	conn := caller.Conn()
	if conn == nil {
		return nil, fmt.Errorf("session attachable conn for client %q was nil", clientID)
	}
	return conn, nil
}

func (srv *Server) sessionMainClientConn(ctx context.Context, sess *daggerSession) (*grpc.ClientConn, error) {
	_ = ctx
	if sess == nil {
		return nil, errors.New("session is nil")
	}
	client, err := srv.clientFromIDs(sess.sessionID, sess.mainClientCallerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get main client %q: %w", sess.mainClientCallerID, err)
	}
	caller, err := client.getClientCaller(sess.mainClientCallerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get main client caller %q: %w", sess.mainClientCallerID, err)
	}
	if caller == nil {
		return nil, fmt.Errorf("main client caller %q was nil", sess.mainClientCallerID)
	}
	conn := caller.Conn()
	if conn == nil {
		return nil, fmt.Errorf("main client conn %q was nil", sess.mainClientCallerID)
	}
	return conn, nil
}

// The nearest ancestor client that is not a module (either a caller from the host like the CLI
// or a nested exec). Useful for figuring out where local sources should be resolved from through
// chains of dependency modules.
func (srv *Server) nonModuleParentClient(ctx context.Context) (*daggerClient, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if client.mod.Self() == nil {
		// not a module client, return the current client
		return client, nil
	}
	for i := len(client.parents) - 1; i >= 0; i-- {
		parent := client.parents[i]
		if parent.mod.Self() == nil {
			// not a module client: match
			return parent, nil
		}
	}
	return nil, fmt.Errorf("no non-module parent found")
}

// The nearest ancestor client that is not a module (either a caller from the host like the CLI
// or a nested exec). Useful for figuring out where local sources should be resolved from through
// chains of dependency modules.
func (srv *Server) NonModuleParentClientMetadata(ctx context.Context) (*engine.ClientMetadata, error) {
	client, err := srv.nonModuleParentClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.clientMetadata, nil
}

// The default deps of every user module (currently just core)
func (srv *Server) DefaultDeps(ctx context.Context) (*core.SchemaBuilder, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.defaultDeps.Clone(), nil
}

func (srv *Server) TelemetrySeenKeyStore(ctx context.Context) (dagql.TelemetrySeenKeyStore, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.daggerSession, nil
}

// The DagQL server for the current client's session
func (srv *Server) Server(ctx context.Context) (*dagql.Server, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.dag, nil
}

// Mix in this http endpoint+handler to the current client's session
func (srv *Server) MuxEndpoint(ctx context.Context, path string, handler http.Handler) error {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return err
	}
	client.daggerSession.endpointMu.Lock()
	defer client.daggerSession.endpointMu.Unlock()
	client.daggerSession.endpoints[path] = handler
	return nil
}

// The auth provider for the current client
func (srv *Server) Auth(ctx context.Context) (*auth.RegistryAuthProvider, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.daggerSession.authProvider, nil
}

// The engine utility client for the current client
func (srv *Server) Engine(ctx context.Context) (*engineutil.Client, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.engineUtilClient, nil
}

func (srv *Server) RegistryResolver(ctx context.Context) (*serverresolver.Resolver, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if client.daggerSession.resolver == nil {
		return nil, errors.New("session registry resolver not initialized")
	}
	return client.daggerSession.resolver, nil
}

// The services for the current client's session
func (srv *Server) Services(ctx context.Context) (*core.Services, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.daggerSession.services, nil
}

// The default platform for the engine as a whole
func (srv *Server) Platform() core.Platform {
	return core.Platform(srv.defaultPlatform)
}

// The content store for the engine as a whole
func (srv *Server) OCIStore() content.Store {
	return srv.contentStore
}

func (srv *Server) BuiltinOCIStore() content.Store {
	return srv.builtinContentStore
}

// The dns configuration for the engine as a whole
func (srv *Server) DNS() *oci.DNSConfig {
	return srv.dns
}

// The lease manager for the engine as a whole
func (srv *Server) LeaseManager() *leaseutil.Manager {
	return srv.leaseManager
}

// A shared engine-wide salt used when creating cache keys for secrets based on their plaintext
func (srv *Server) SecretSalt() []byte {
	return srv.secretSalt
}

// Provides access to the client's telemetry database.
func (srv *Server) FlushSessionTelemetry(ctx context.Context) error {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return err
	}
	return client.daggerSession.FlushTelemetry(ctx)
}

func (srv *Server) ClientTelemetry(ctx context.Context, sessID, clientID string) (*clientdb.DB, error) {
	client, err := srv.clientFromIDs(sessID, clientID)
	if err != nil {
		return nil, err
	}
	// Flush ALL clients in the session, not just the requested one.
	// Spans from nested clients may still be buffered in their
	// BatchSpanProcessor. A session-wide flush ensures the span tree
	// is complete before captureLogs walks it via SelectLogsBeneathSpan.
	if err := client.daggerSession.FlushTelemetry(ctx); err != nil {
		return nil, fmt.Errorf("flush telemetry: %w", err)
	}
	return client.TelemetryDB(ctx)
}

// Return a client connected to a cloud engine. If bool return is false, the local engine should be used. Session attachables for the returned client will be proxied back to the calling client.
func (srv *Server) CloudEngineClient(
	ctx context.Context,
	module string,
	function string,
	execCmd []string,
) (*engineclient.Client, bool, error) {
	parentClient, err := srv.nonModuleParentClient(ctx)
	if err != nil {
		return nil, false, err
	}
	parentCallerCtx := engine.ContextWithClientMetadata(ctx, parentClient.clientMetadata)
	parentSession, err := parentClient.engineUtilClient.GetSessionCaller(parentCallerCtx, false)
	if err != nil {
		return nil, false, err
	}

	// TODO: cloud support for "run on yourself", return (nil, false, nil) in that case

	engineClient, err := engineclient.ConnectEngineToEngine(ctx, engineclient.EngineToEngineParams{
		Params: engineclient.Params{
			RunnerHost: engine.DefaultCloudRunnerHost,

			Module:   module,
			Function: function,
			ExecCmd:  execCmd,

			CloudAuth: parentClient.clientMetadata.CloudAuth,

			EngineTrace:   parentClient.spanExporter,
			EngineLogs:    parentClient.logExporter,
			EngineMetrics: []sdkmetric.Exporter{parentClient.metricExporter},

			// FIXME: for now, disable recursive scale out to prevent any
			// surprise "fork-bomb" scenarios. Eventually this should be
			// permitted.
			EnableCloudScaleOut: false,
		},
		CallerSessionConn: parentSession.Conn(),
		Labels:            enginetel.NewLabels(parentClient.clientMetadata.Labels, nil, nil),
		StableClientID:    parentClient.clientMetadata.ClientStableID,
	})
	if err != nil {
		return nil, false, err
	}

	return engineClient, true, nil
}

// A mount namespace guaranteed to not have any mounts created by engine operations.
// Should be used when creating goroutines/processes that unshare a mount namespace,
// otherwise those unshared mnt namespaces may inherit mounts from engine operations
// and leak them.
func (srv *Server) CleanMountNS() *os.File {
	return srv.cleanMntNS
}

type httpError struct {
	error
	code int
}

func httpErr(err error, code int) httpError {
	return httpError{err, code}
}

func (e httpError) WriteTo(w http.ResponseWriter) {
	http.Error(w, e.Error(), e.code)
}

type gqlError struct {
	error
	httpCode int
}

func gqlErr(err error, httpCode int) gqlError {
	return gqlError{err, httpCode}
}

func (e gqlError) WriteTo(w http.ResponseWriter) {
	gqlerr := &gqlerror.Error{
		Err:     e.error,
		Message: e.Error(),
	}
	res := graphql.Response{
		Errors: gqlerror.List{gqlerr},
	}
	bytes, err := json.Marshal(res)
	if err != nil {
		panic(err)
	}
	http.Error(w, string(bytes), e.httpCode)
}

// httpHandlerFunc lets you write an http handler that just returns an error, which will be
// turned into a 500 http response if non-nil, or a specific code if the error is of type httpError.
// It also accepts a generic extra argument that will be passed to the handler function to support
// providing any extra already initialized state.
func httpHandlerFunc[T any](fn func(http.ResponseWriter, *http.Request, T) error, t T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := fn(w, r, t)
		if err == nil {
			return
		}

		bklog.G(r.Context()).
			WithField("method", r.Method).
			WithField("path", r.URL.Path).
			WithError(err).Error("failed to serve request")

		// check whether this is a hijacked connection, if so we can't write any http errors to it
		if _, testErr := w.Write(nil); testErr == http.ErrHijacked {
			return
		}

		var httpErr httpError
		var gqlErr gqlError
		switch {
		case errors.As(err, &httpErr):
			httpErr.WriteTo(w)
		case errors.As(err, &gqlErr):
			gqlErr.WriteTo(w)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
