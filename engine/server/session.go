package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"dagger.io/dagger/telemetry"
	"github.com/Khan/genqlient/graphql"
	"github.com/containerd/containerd/content"
	"github.com/koron-go/prefixw"
	"github.com/moby/buildkit/cache/remotecache"
	bkclient "github.com/moby/buildkit/client"
	bkfrontend "github.com/moby/buildkit/frontend"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bksession "github.com/moby/buildkit/session"
	bksolver "github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/llbsolver"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/moby/buildkit/util/progress/progressui"
	"github.com/opencontainers/go-digest"
	"github.com/sirupsen/logrus"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
	"resenje.org/singleflight"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/cache/cachemanager"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
)

type daggerSession struct {
	sessionID          string
	mainClientCallerID string

	state   daggerSessionState
	stateMu sync.RWMutex

	clients  map[string]*daggerClient // clientID -> client
	clientMu sync.RWMutex

	// closed after the shutdown endpoint is called
	shutdownCh        chan struct{}
	closeShutdownOnce sync.Once

	// the http endpoints being served (as a map since APIs like shellEndpoint can add more)
	endpoints  map[string]http.Handler
	endpointMu sync.RWMutex

	// informed when a client goes away to prevent hanging on drain
	telemetryPubSub *PubSub

	services *core.Services

	analytics analytics.Tracker

	authProvider *auth.RegistryAuthProvider

	cacheExporterCfgs []bkgw.CacheOptionsEntry
	cacheImporterCfgs []bkgw.CacheOptionsEntry

	refs   map[buildkit.Reference]struct{}
	refsMu sync.Mutex

	containers   map[bkgw.Container]struct{}
	containersMu sync.Mutex

	dagqlCache dagql.Cache

	interactive        bool
	interactiveCommand []string
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

	secretStore *core.SecretStore
	socketStore *core.SocketStore

	dagqlRoot *core.Query

	// if the client is coming from a module, this is that module
	mod *core.Module
	// during module initialization, we don't have a full module yet but need to call
	// a function to get the module typedefs. In this case mod is nil but modName
	// will be set to allow SDKs to get the module name during that call if needed.
	modName string

	// the DAG of modules being served to this client
	deps *core.ModDeps
	// the default deps that each client/module starts out with (currently just core)
	defaultDeps *core.ModDeps

	// If the client is itself from a function call in a user module, this is set with the
	// metadata of that ongoing function call
	fnCall *core.FunctionCall

	// buildkit job-related state/config
	buildkitSession *bksession.Session
	getClientCaller func(string) (bksession.Caller, error)
	job             *bksolver.Job
	llbSolver       *llbsolver.Solver
	llbBridge       bkfrontend.FrontendLLBBridge
	dialer          *net.Dialer
	bkClient        *buildkit.Client

	// SQLite database storing telemetry + anything else
	db             *sql.DB
	tracerProvider *sdktrace.TracerProvider
	loggerProvider *sdklog.LoggerProvider
	meterProvider  *sdkmetric.MeterProvider
}

type daggerClientState string

const (
	clientStateUninitialized daggerClientState = "uninitialized"
	clientStateInitialized   daggerClientState = "initialized"
)

func (client *daggerClient) String() string {
	return fmt.Sprintf("<Client %s: %s>", client.clientID, client.state)
}

func (client *daggerClient) FlushTelemetry(ctx context.Context) error {
	slog := slog.With("client", client.clientID)
	var errs error
	if client.tracerProvider != nil {
		slog.ExtraDebug("force flushing client traces")
		// FIXME: mitigation for goroutine leak fixed upstream in
		// https://github.com/open-telemetry/opentelemetry-go/pull/6363
		// Just give this context a real generous timeout for now so if we
		// are canceled we don't leak
		// Can undo this once we've picked up the upstream fix.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
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
	failureCleanups *buildkit.Cleanups,
) error {
	slog.ExtraDebug("initializing new session", "session", clientMetadata.SessionID)
	defer slog.ExtraDebug("initialized new session", "session", clientMetadata.SessionID)

	sess.sessionID = clientMetadata.SessionID
	sess.mainClientCallerID = clientMetadata.ClientID
	sess.clients = map[string]*daggerClient{}
	sess.endpoints = map[string]http.Handler{}
	sess.shutdownCh = make(chan struct{})
	sess.services = core.NewServices()
	sess.authProvider = auth.NewRegistryAuthProvider()
	sess.refs = map[buildkit.Reference]struct{}{}
	sess.containers = map[bkgw.Container]struct{}{}
	sess.dagqlCache = dagql.NewCache()
	sess.telemetryPubSub = srv.telemetryPubSub
	sess.interactive = clientMetadata.Interactive
	sess.interactiveCommand = clientMetadata.InteractiveCommand

	sess.analytics = analytics.New(analytics.Config{
		DoNotTrack: clientMetadata.DoNotTrack || analytics.DoNotTrack(),
		Labels: enginetel.Labels(clientMetadata.Labels).
			WithEngineLabel(srv.engineName).
			WithServerLabels(
				engine.Version,
				runtime.GOOS,
				runtime.GOARCH,
				srv.SolverCache.ID() != cachemanager.LocalCacheID,
			),
		CloudToken: clientMetadata.CloudToken,
	})
	failureCleanups.Add("close session analytics", sess.analytics.Close)

	for _, cacheImportCfg := range clientMetadata.UpstreamCacheImportConfig {
		_, ok := srv.cacheImporters[cacheImportCfg.Type]
		if !ok {
			return fmt.Errorf("unknown cache importer type %q", cacheImportCfg.Type)
		}
		sess.cacheImporterCfgs = append(sess.cacheImporterCfgs, bkgw.CacheOptionsEntry{
			Type:  cacheImportCfg.Type,
			Attrs: cacheImportCfg.Attrs,
		})
	}
	for _, cacheExportCfg := range clientMetadata.UpstreamCacheExportConfig {
		_, ok := srv.cacheExporters[cacheExportCfg.Type]
		if !ok {
			return fmt.Errorf("unknown cache exporter type %q", cacheExportCfg.Type)
		}
		sess.cacheExporterCfgs = append(sess.cacheExporterCfgs, bkgw.CacheOptionsEntry{
			Type:  cacheExportCfg.Type,
			Attrs: cacheExportCfg.Attrs,
		})
	}

	sess.state = sessionStateInitialized
	return nil
}

func (sess *daggerSession) withShutdownCancel(ctx context.Context) context.Context {
	ctx, cancel := context.WithCancelCause(ctx)
	go func() {
		<-sess.shutdownCh
		cancel(errors.New("session shutdown called"))
	}()
	return ctx
}

// requires that sess.stateMu is held
func (srv *Server) removeDaggerSession(ctx context.Context, sess *daggerSession) error {
	slog := slog.With("session", sess.sessionID)

	slog.Debug("removing session; stopping client services and flushing")
	defer slog.Debug("session removed")

	// check if the local cache needs pruning after session is removed, prune if so
	defer func() {
		time.AfterFunc(time.Second, srv.throttledGC)
	}()

	srv.daggerSessionsMu.Lock()
	delete(srv.daggerSessions, sess.sessionID)
	srv.daggerSessionsMu.Unlock()

	sess.state = sessionStateRemoved

	var errs error

	// in theory none of this should block very long, but add a safeguard just in case
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()

	if err := sess.dagqlCache.ReleaseAll(ctx); err != nil {
		slog.Error("error releasing dagql cache", "error", err)
		errs = errors.Join(errs, fmt.Errorf("release dagql cache: %w", err))
	}

	if err := sess.services.StopSessionServices(ctx, sess.sessionID); err != nil {
		slog.Warn("error stopping services", "error", err)
		errs = errors.Join(errs, fmt.Errorf("stop client services: %w", err))
	}

	slog.Debug("stopped services")

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
			client.job.Discard()
			client.job.CloseProgress()

			if client.llbSolver != nil {
				errs = errors.Join(errs, client.llbSolver.Close())
				client.llbSolver = nil
			}

			if client.buildkitSession != nil {
				errs = errors.Join(errs, client.buildkitSession.Close())
				client.buildkitSession = nil
			}

			// Flush all telemetry.
			errs = errors.Join(errs, client.ShutdownTelemetry(ctx))

			// Close client DB for writing; subscribers will have their own connection
			errs = errors.Join(errs, client.db.Close())

			return errs
		})
	}
	errs = errors.Join(errs, releaseGroup.Wait())

	// release all the references solved in the session
	sess.refsMu.Lock()
	var refReleaseGroup errgroup.Group
	for rf := range sess.refs {
		if rf != nil {
			refReleaseGroup.Go(func() error {
				return rf.Release(ctx)
			})
		}
	}
	errs = errors.Join(errs, refReleaseGroup.Wait())
	sess.refs = nil
	sess.refsMu.Unlock()

	// cleanup analytics and telemetry
	errs = errors.Join(errs, sess.analytics.Close())

	// ensure this chan is closed even if the client never explicitly called the /shutdown endpoint
	sess.closeShutdownOnce.Do(func() {
		close(sess.shutdownCh)
	})
	return errs
}

type ClientInitOpts struct {
	*engine.ClientMetadata

	// If this is a nested client, the call that created the client (i.e. a function call or
	// an exec with nesting enabled)
	CallID *call.ID

	// If this is a nested client, the client ID of the caller that created it
	CallerClientID string

	// If the client is running from a function in a module, this is the encoded dagQL ID
	// of that module.
	EncodedModuleID string

	// If the client is running from a function in a module, this is the encoded function call
	// metadata (of type core.FunctionCall)
	EncodedFunctionCall json.RawMessage

	// Client resource IDs passed to this client from parent object fields.
	// Needed to handle finding any secrets, sockets or other client resources
	// that this client should have access to due to being set in the parent
	// object.
	ParentIDs map[digest.Digest]*resource.ID

	// corner case: when initializing a module by calling it to get its typedefs, we don't actually
	// have a full EncodedModuleID yet, but some SDKs still call CurrentModule.name then. For this
	// case we just provide the ModuleName and use that to support CurrentModule.name.
	ModuleName string
}

// requires that client.stateMu is held
func (srv *Server) initializeDaggerClient(
	ctx context.Context,
	client *daggerClient,
	failureCleanups *buildkit.Cleanups,
	opts *ClientInitOpts,
) error {
	// initialize all the buildkit+session attachable state for the client
	client.secretStore = core.NewSecretStore(srv.bkSessionManager)
	client.socketStore = core.NewSocketStore(srv.bkSessionManager)
	if opts.CallID != nil {
		if opts.CallerClientID == "" {
			return fmt.Errorf("caller client ID is not set")
		}
		if err := srv.addClientResourcesFromID(ctx, client, &resource.ID{ID: *opts.CallID}, opts.CallerClientID, true); err != nil {
			return fmt.Errorf("failed to add client resources from ID: %w", err)
		}
	}
	if opts.ParentIDs != nil {
		if opts.CallerClientID == "" {
			return fmt.Errorf("caller client ID is not set")
		}
		// we can use the caller client ID here (as opposed to the client ID of the parent function call)
		// because any client resources returned by the parent function call will be added to the stores
		// of the caller
		for _, id := range opts.ParentIDs {
			if err := srv.addClientResourcesFromID(ctx, client, id, opts.CallerClientID, false); err != nil {
				return fmt.Errorf("failed to add parent client resources from ID: %w", err)
			}
		}
	}

	wc, err := buildkit.AsWorkerController(srv.worker)
	if err != nil {
		return err
	}
	client.llbSolver, err = llbsolver.New(llbsolver.Opt{
		WorkerController: wc,
		Frontends:        srv.frontends,
		CacheManager:     srv.SolverCache,
		SessionManager:   srv.bkSessionManager,
		CacheResolvers:   srv.cacheImporters,
		Entitlements:     buildkit.ToEntitlementStrings(srv.entitlements),
	})
	if err != nil {
		return fmt.Errorf("failed to create llbsolver: %w", err)
	}
	failureCleanups.Add("close llb solver", client.llbSolver.Close)

	var callerG singleflight.Group[string, bksession.Caller]
	client.getClientCaller = func(id string) (bksession.Caller, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		caller, _, err := callerG.Do(ctx, id, func(ctx context.Context) (bksession.Caller, error) {
			return srv.bkSessionManager.Get(ctx, id, false)
		})
		return caller, err
	}

	client.buildkitSession, err = srv.newBuildkitSession(ctx, client)
	if err != nil {
		return fmt.Errorf("failed to create buildkit session: %w", err)
	}
	failureCleanups.Add("close buildkit session", client.buildkitSession.Close)

	client.job, err = srv.solver.NewJob(client.buildkitSession.ID())
	if err != nil {
		return fmt.Errorf("failed to create buildkit job: %w", err)
	}
	failureCleanups.Add("discard solver job", client.job.Discard)
	failureCleanups.Add("stop solver progress", buildkit.Infallible(client.job.CloseProgress))

	client.job.SessionID = client.buildkitSession.ID()
	client.job.SetValue(buildkit.EntitlementsJobKey, srv.entitlements)

	br := client.llbSolver.Bridge(client.job)
	client.llbBridge = br

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

	// write progress for extra debugging if configured
	bkLogsW := srv.buildkitLogSink
	if bkLogsW != nil {
		prefix := fmt.Sprintf("[buildkit] [client=%s] ", client.clientID)
		bkLogsW = prefixw.New(bkLogsW, prefix)
		statusCh := make(chan *bkclient.SolveStatus, 8)
		pw, err := progressui.NewDisplay(bkLogsW, progressui.PlainMode)
		if err != nil {
			return fmt.Errorf("failed to create progress writer: %w", err)
		}
		// ensure these logs keep getting printed until the session goes away, not just until this request finishes
		logCtx := client.daggerSession.withShutdownCancel(context.WithoutCancel(ctx))
		go client.job.Status(logCtx, statusCh)
		go pw.UpdateFrom(logCtx, statusCh)
	}

	client.bkClient, err = buildkit.NewClient(ctx, &buildkit.Opts{
		Worker:               srv.worker,
		SessionManager:       srv.bkSessionManager,
		BkSession:            client.buildkitSession,
		LLBBridge:            client.llbBridge,
		Dialer:               client.dialer,
		GetClientCaller:      client.getClientCaller,
		GetMainClientCaller:  client.getMainClientCaller,
		Entitlements:         srv.entitlements,
		UpstreamCacheImports: client.daggerSession.cacheImporterCfgs,
		Frontends:            srv.frontends,

		Refs:         client.daggerSession.refs,
		RefsMu:       &client.daggerSession.refsMu,
		Containers:   client.daggerSession.containers,
		ContainersMu: &client.daggerSession.containersMu,

		Interactive:        client.daggerSession.interactive,
		InteractiveCommand: client.daggerSession.interactiveCommand,
	})
	if err != nil {
		return fmt.Errorf("failed to create buildkit client: %w", err)
	}

	// setup the graphql server + module/function state for the client
	client.dagqlRoot = core.NewRoot(srv)

	dag := dagql.NewServer(client.dagqlRoot)
	dag.Cache = client.daggerSession.dagqlCache
	dag.Around(core.AroundFunc)
	coreMod := &schema.CoreMod{Dag: dag}
	if err := coreMod.Install(ctx, dag); err != nil {
		return fmt.Errorf("failed to install core module: %w", err)
	}
	client.defaultDeps = core.NewModDeps(client.dagqlRoot, []core.Mod{coreMod})

	client.modName = opts.ModuleName

	if opts.EncodedModuleID == "" {
		client.deps = core.NewModDeps(client.dagqlRoot, []core.Mod{coreMod})
		coreMod.Dag.View = engine.BaseVersion(engine.NormalizeVersion(client.clientVersion))
	} else {
		modID := new(call.ID)
		if err := modID.Decode(opts.EncodedModuleID); err != nil {
			return fmt.Errorf("failed to decode module ID: %w", err)
		}
		modInst, err := dagql.NewID[*core.Module](modID).Load(ctx, coreMod.Dag)
		if err != nil {
			return fmt.Errorf("failed to load module: %w", err)
		}
		client.mod = modInst.Self

		// this is needed to set the view of the core api as compatible
		// with the module we're currently calling from
		engineVersion := client.mod.Source.Self.EngineVersion
		coreMod.Dag.View = engine.BaseVersion(engine.NormalizeVersion(engineVersion))

		// NOTE: *technically* we should reload the module here, so that we can
		// use the new typedefs api - but at this point we likely would
		// have failed to load the module in the first place anyways?
		// modInst, err = dagql.NewID[*core.Module](modID).Load(ctx, coreMod.Dag)
		// if err != nil {
		// 	return fmt.Errorf("failed to load module: %w", err)
		// }
		// client.mod = modInst.Self

		client.deps = core.NewModDeps(client.dagqlRoot, client.mod.Deps.Mods)
		// if the module has any of it's own objects defined, serve its schema to itself too
		if len(client.mod.ObjectDefs) > 0 {
			client.deps = client.deps.Append(client.mod)
		}
		client.defaultDeps = core.NewModDeps(client.dagqlRoot, []core.Mod{coreMod})
	}

	if opts.EncodedFunctionCall != nil {
		var fnCall core.FunctionCall
		if err := json.Unmarshal(opts.EncodedFunctionCall, &fnCall); err != nil {
			return fmt.Errorf("failed to decode function call: %w", err)
		}
		fnCall.Query = client.dagqlRoot
		client.fnCall = &fnCall
	}

	// configure OTel providers that export to SQLite
	tracerOpts := []sdktrace.TracerProviderOption{
		// install a span processor that modifies spans created by Buildkit to
		// fit our ideal format
		sdktrace.WithSpanProcessor(buildkit.NewSpanProcessor(
			client.bkClient,
		)),
		// save to our own client's DB
		sdktrace.WithSpanProcessor(telemetry.NewLiveSpanProcessor(
			srv.telemetryPubSub.Spans(client),
		)),
	}
	loggerOpts := []sdklog.LoggerProviderOption{
		sdklog.WithResource(telemetry.Resource),
		sdklog.WithProcessor(
			sdklog.NewBatchProcessor(
				srv.telemetryPubSub.Logs(client),
				sdklog.WithExportInterval(telemetry.NearlyImmediate),
			),
		),
	}

	const metricReaderInterval = 1 * time.Second

	meterOpts := []sdkmetric.Option{
		sdkmetric.WithResource(telemetry.Resource),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
			srv.telemetryPubSub.Metrics(client),
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
			sdklog.NewBatchProcessor(
				srv.telemetryPubSub.Logs(parent),
				sdklog.WithExportInterval(telemetry.NearlyImmediate),
			),
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

func (srv *Server) clientFromContext(ctx context.Context) (*daggerClient, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata for session call: %w", err)
	}
	client, ok := srv.clientFromIDs(clientMetadata.SessionID, clientMetadata.ClientID)
	if !ok {
		return nil, fmt.Errorf("client %q not found", clientMetadata.ClientID)
	}
	return client, nil
}

func (srv *Server) clientFromIDs(sessID, clientID string) (*daggerClient, bool) {
	srv.daggerSessionsMu.RLock()
	defer srv.daggerSessionsMu.RUnlock()
	sess, ok := srv.daggerSessions[sessID]
	if !ok {
		return nil, false
	}

	sess.clientMu.RLock()
	defer sess.clientMu.RUnlock()
	client, ok := sess.clients[clientID]
	if !ok {
		return nil, false
	}

	return client, true
}

// initialize session+client if needed, return:
// * the initialized client
// * a cleanup func to run when the call is done
func (srv *Server) getOrInitClient(
	ctx context.Context,
	opts *ClientInitOpts,
) (_ *daggerClient, _ func() error, rerr error) {
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
	failureCleanups := &buildkit.Cleanups{}
	defer func() {
		if rerr != nil {
			rerr = errors.Join(rerr, failureCleanups.Run())
		}
	}()

	// get or initialize the session as a whole

	srv.daggerSessionsMu.Lock()
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
		var err error
		client.db, err = srv.clientDBs.Create(client.clientID)
		if err != nil {
			return nil, nil, fmt.Errorf("open client DB: %w", err)
		}

		parent, parentExists := sess.clients[opts.CallerClientID]
		if parentExists {
			client.parents = append([]*daggerClient{}, parent.parents...)
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
		if err := srv.initializeDaggerClient(ctx, client, failureCleanups, opts); err != nil {
			return nil, nil, fmt.Errorf("initialize client: %w", err)
		}
	case clientStateInitialized:
		// verify token matches existing client
		if token != client.secretToken {
			return nil, nil, fmt.Errorf("client %q already exists with different secret token", clientID)
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
				"session", sess.sessionID,
				"state", sess.state,
			)
			return nil
		}
	}, nil
}

func (srv *Server) getClient(sessionID, clientID string) (*daggerClient, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("missing session ID")
	}
	if clientID == "" {
		return nil, fmt.Errorf("missing client ID")
	}
	srv.daggerSessionsMu.RLock()
	sess, ok := srv.daggerSessions[sessionID]
	srv.daggerSessionsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	sess.clientMu.RLock()
	client, ok := sess.clients[clientID]
	sess.clientMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("client %q not found", clientID)
	}
	return client, nil
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

// ServeHTTPToNestedClient serves nested clients, including module function calls. The only difference is that additional
// execution metadata is passed alongside the request from the executor. We don't want to put all this execution metadata
// in http headers since it includes arbitrary values from users in the function call metadata, which can exceed max header
// size.
func (srv *Server) ServeHTTPToNestedClient(w http.ResponseWriter, r *http.Request, execMD *buildkit.ExecutionMetadata) {
	clientVersion := engine.Version
	if md, _ := engine.ClientMetadataFromHTTPHeaders(r.Header); md != nil {
		clientVersion = md.ClientVersion
	}

	httpHandlerFunc(srv.serveHTTPToClient, &ClientInitOpts{
		ClientMetadata: &engine.ClientMetadata{
			ClientID:          execMD.ClientID,
			ClientVersion:     clientVersion,
			ClientSecretToken: execMD.SecretToken,
			SessionID:         execMD.SessionID,
			ClientHostname:    execMD.Hostname,
			ClientStableID:    execMD.ClientStableID,
			Labels:            map[string]string{},
			SSHAuthSocketPath: execMD.SSHAuthSocketPath,
		},
		CallID:              execMD.CallID,
		CallerClientID:      execMD.CallerClientID,
		EncodedModuleID:     execMD.EncodedModuleID,
		EncodedFunctionCall: execMD.EncodedFunctionCall,
		ParentIDs:           execMD.ParentIDs,
		ModuleName:          execMD.ModuleName,
	}).ServeHTTP(w, r)
}

const InstrumentationLibrary = "dagger.io/engine.server"

func (srv *Server) DagqlServer(ctx context.Context) (*dagql.Server, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	schema, err := client.deps.Schema(ctx)
	if err != nil {
		return nil, err
	}
	return schema, nil
}

func (srv *Server) serveHTTPToClient(w http.ResponseWriter, r *http.Request, opts *ClientInitOpts) (rerr error) {
	ctx := r.Context()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(fmt.Errorf("http request done for client %q", opts.ClientID))

	clientMetadata := opts.ClientMetadata
	ctx = engine.ContextWithClientMetadata(ctx, clientMetadata)

	// propagate span context from the client
	ctx = telemetry.Propagator.Extract(ctx, propagation.HeaderCarrier(r.Header))

	ctx = bklog.WithLogger(ctx, bklog.G(ctx).
		WithField("client_id", clientMetadata.ClientID).
		WithField("client_hostname", clientMetadata.ClientHostname).
		WithField("session_id", clientMetadata.SessionID))

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
		client, err := srv.getClient(clientMetadata.SessionID, clientMetadata.ClientID)
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

	ctx = client.daggerSession.withShutdownCancel(ctx)

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
	ctx := r.Context()

	// only record telemetry if the request is traced, otherwise
	// we end up with orphaned spans in their own separate traces from tests etc.
	if trace.SpanContextFromContext(ctx).IsValid() {
		// create a span to record telemetry into the client's DB
		//
		// downstream components must use otel.SpanFromContext(ctx).TracerProvider()
		clientTracer := client.tracerProvider.Tracer(InstrumentationLibrary)
		var span trace.Span
		ctx, span = clientTracer.Start(ctx,
			fmt.Sprintf("%s %s", r.Method, r.URL.Path),
			trace.WithAttributes(attribute.Bool(telemetry.UIPassthroughAttr, true)),
		)
		defer telemetry.End(span, func() error { return rerr })
	}

	// install a logger+meter provider that records to the client's DB
	ctx = telemetry.WithLoggerProvider(ctx, client.loggerProvider)
	ctx = telemetry.WithMeterProvider(ctx, client.meterProvider)
	r = r.WithContext(ctx)

	// get the schema we're gonna serve to this client based on which modules they have loaded, if any
	schema, err := client.deps.Schema(ctx)
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

	// turn panics into graphql errors
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

	sess := client.daggerSession
	slog := slog.With(
		"isMainClient", client.clientID == sess.mainClientCallerID,
		"sessionID", sess.sessionID,
		"clientID", client.clientID,
		"mainClientID", sess.mainClientCallerID)

	slog.Trace("shutting down server")
	defer slog.Trace("done shutting down server")

	if client.clientID == sess.mainClientCallerID {
		slog.Debug("main client is shutting down")

		// Stop services, since the main client is going away, and we
		// want the client to see them stop.
		sess.services.StopSessionServices(ctx, sess.sessionID)

		if len(sess.cacheExporterCfgs) > 0 {
			ctx = context.WithoutCancel(ctx)
			t := client.tracerProvider.Tracer(InstrumentationLibrary)
			ctx, span := t.Start(ctx, "cache export", telemetry.Encapsulate())
			defer span.End()

			// create an internal span so we hide exporter children spans which are quite noisy
			ctx, cInternal := t.Start(ctx, "cache export internal", telemetry.Internal())
			defer cInternal.End()
			bklog.G(ctx).Debugf("running cache export for client %s", client.clientID)
			cacheExporterFuncs := make([]buildkit.ResolveCacheExporterFunc, len(sess.cacheExporterCfgs))
			for i, cacheExportCfg := range sess.cacheExporterCfgs {
				cacheExporterFuncs[i] = func(ctx context.Context, sessionGroup bksession.Group) (remotecache.Exporter, error) {
					exporterFunc, ok := srv.cacheExporters[cacheExportCfg.Type]
					if !ok {
						return nil, fmt.Errorf("unknown cache exporter type %q", cacheExportCfg.Type)
					}
					return exporterFunc(ctx, sessionGroup, cacheExportCfg.Attrs)
				}
			}
			err := client.bkClient.UpstreamCacheExport(ctx, cacheExporterFuncs)
			if err != nil {
				bklog.G(ctx).WithError(err).Errorf("error running cache export for client %s", client.clientID)
			}
			bklog.G(ctx).Debugf("done running cache export for client %s", client.clientID)
		}

		defer func() {
			// Signal shutdown at the very end, _after_ flushing telemetry/etc.,
			// so we can respect the shutdownCh to short-circuit any telemetry
			// subscribers that appeared _while_ shutting down.
			sess.closeShutdownOnce.Do(func() {
				close(sess.shutdownCh)
			})
		}()
	}

	// Flush telemetry across the entire session so that any child clients will
	// save telemetry into their parent's DB, including to this client.
	slog.ExtraDebug("flushing session telemetry")
	if err := sess.FlushTelemetry(ctx); err != nil {
		slog.Error("failed to flush telemetry", "error", err)
	}

	client.closeShutdownOnce.Do(func() {
		close(client.shutdownCh)
	})

	return nil
}

// Stitch in the given module to the list being served to the current client
func (srv *Server) ServeModule(ctx context.Context, mod *core.Module) error {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return err
	}

	client.stateMu.Lock()
	defer client.stateMu.Unlock()

	// don't add the same module twice
	// This can happen with generated clients since all remote dependencies are added
	// on each connection and this could happen multiple times.
	depMod, exist := client.deps.LookupDep(mod.Name())
	if exist {
		// Error if there's a conflict between dependencies
		if depMod.GetSource().AsString() != "" && mod.Source.Self.AsString() != depMod.GetSource().AsString() {
			return fmt.Errorf("module %s already exists with different source %s", mod.Name(), depMod.GetSource().AsString())
		}

		return nil
	}

	client.deps = client.deps.Append(mod)
	return nil
}

// If the current client is coming from a function, return the module that function is from
func (srv *Server) CurrentModule(ctx context.Context) (*core.Module, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if client.clientID == client.daggerSession.mainClientCallerID {
		return nil, fmt.Errorf("%w: main client caller has no current module", core.ErrNoCurrentModule)
	}
	if client.mod != nil {
		return client.mod, nil
	}

	if client.modName != "" {
		return &core.Module{NameField: client.modName}, nil
	}

	return nil, core.ErrNoCurrentModule
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

// Return the list of deps being served to the current client
func (srv *Server) CurrentServedDeps(ctx context.Context) (*core.ModDeps, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.deps, nil
}

// The ClientID of the main client caller (i.e. the one who created the session, typically the CLI
// invoked by the user)
func (srv *Server) MainClientCallerID(ctx context.Context) (string, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return "", err
	}
	return client.daggerSession.mainClientCallerID, nil
}

// The default deps of every user module (currently just core)
func (srv *Server) DefaultDeps(ctx context.Context) (*core.ModDeps, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.defaultDeps.Clone(), nil
}

// The DagQL query cache for the current client's session
func (srv *Server) Cache(ctx context.Context) (dagql.Cache, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.daggerSession.dagqlCache, nil
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

// The secret store for the current client
func (srv *Server) Secrets(ctx context.Context) (*core.SecretStore, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.secretStore, nil
}

// The socket store for the current client
func (srv *Server) Sockets(ctx context.Context) (*core.SocketStore, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.socketStore, nil
}

// The auth provider for the current client
func (srv *Server) Auth(ctx context.Context) (*auth.RegistryAuthProvider, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.daggerSession.authProvider, nil
}

// The buildkit APIs for the current client
func (srv *Server) Buildkit(ctx context.Context) (*buildkit.Client, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.bkClient, nil
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

// The lease manager for the engine as a whole
func (srv *Server) LeaseManager() *leaseutil.Manager {
	return srv.leaseManager
}

// The nearest ancestor client that is not a module (either a caller from the host like the CLI
// or a nested exec). Useful for figuring out where local sources should be resolved from through
// chains of dependency modules.
func (srv *Server) NonModuleParentClientMetadata(ctx context.Context) (*engine.ClientMetadata, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if client.mod == nil {
		// not a module client, return the metadata
		return client.clientMetadata, nil
	}
	for i := len(client.parents) - 1; i >= 0; i-- {
		parent := client.parents[i]
		if parent.mod == nil {
			// not a module client, return the metadata
			return parent.clientMetadata, nil
		}
	}

	return nil, fmt.Errorf("no non-module parent found")
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
