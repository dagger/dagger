package server

import (
	"context"
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
	"github.com/99designs/gqlgen/graphql/handler"
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
	"github.com/sirupsen/logrus"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/cache"
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
	telemetryPubSub *enginetel.PubSub

	services *core.Services

	analytics analytics.Tracker

	secretStore  *core.SecretStore
	authProvider *auth.RegistryAuthProvider

	cacheExporterCfgs []bkgw.CacheOptionsEntry
	cacheImporterCfgs []bkgw.CacheOptionsEntry

	refs   map[buildkit.Reference]struct{}
	refsMu sync.Mutex

	containers   map[bkgw.Container]struct{}
	containersMu sync.Mutex

	dagqlCache       dagql.Cache
	cacheEntrySetMap *sync.Map

	interactive bool
}

type daggerSessionState string

const (
	sessionStateUninitialized daggerSessionState = "uninitialized"
	sessionStateInitialized   daggerSessionState = "initialized"
	sessionStateRemoved       daggerSessionState = "removed"
)

type daggerClient struct {
	daggerSession *daggerSession
	clientID      string
	clientVersion string
	secretToken   string

	state   daggerClientState
	stateMu sync.RWMutex
	// the number of active http requests to any endpoint from this client,
	// used to determine when to cleanup the client+session
	activeCount int

	dagqlRoot *core.Query

	// if the client is coming from a module, this is that module
	mod *core.Module

	// the DAG of modules being served to this client
	deps *core.ModDeps
	// the default deps that each client/module starts out with (currently just core)
	defaultDeps *core.ModDeps

	// If the client is itself from a function call in a user module, this is set with the
	// metadata of that ongoing function call
	fnCall *core.FunctionCall

	// buildkit job-related state/config
	buildkitSession     *bksession.Session
	getMainClientCaller func() (bksession.Caller, error)
	job                 *bksolver.Job
	llbSolver           *llbsolver.Solver
	llbBridge           bkfrontend.FrontendLLBBridge
	dialer              *net.Dialer
	spanCtx             trace.SpanContext
	bkClient            *buildkit.Client
}

type daggerClientState string

const (
	clientStateUninitialized daggerClientState = "uninitialized"
	clientStateInitialized   daggerClientState = "initialized"
)

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
	sess.secretStore = core.NewSecretStore()
	sess.authProvider = auth.NewRegistryAuthProvider()
	sess.refs = map[buildkit.Reference]struct{}{}
	sess.containers = map[bkgw.Container]struct{}{}
	sess.dagqlCache = dagql.NewCache()
	sess.cacheEntrySetMap = &sync.Map{}
	sess.telemetryPubSub = srv.telemetryPubSub
	sess.interactive = clientMetadata.Interactive

	sess.analytics = analytics.New(analytics.Config{
		DoNotTrack: clientMetadata.DoNotTrack || analytics.DoNotTrack(),
		Labels: enginetel.Labels(clientMetadata.Labels).
			WithEngineLabel(srv.engineName).
			WithServerLabels(
				engine.Version,
				runtime.GOOS,
				runtime.GOARCH,
				srv.SolverCache.ID() != cache.LocalCacheID,
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
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		<-sess.shutdownCh
		cancel()
	}()
	return ctx
}

// requires that sess.stateMu is held
func (srv *Server) removeDaggerSession(ctx context.Context, sess *daggerSession) error {
	slog.Debug("session closing; stopping client services and flushing",
		"session", sess.sessionID,
	)
	defer slog.Debug("session closed",
		"session", sess.sessionID,
	)

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

	if err := sess.services.StopSessionServices(ctx, sess.sessionID); err != nil {
		errs = errors.Join(errs, fmt.Errorf("stop client services: %w", err))
	}

	// release containers + buildkit solver/session state in parallel

	var releaseGroup errgroup.Group
	sess.containersMu.Lock()
	defer sess.containersMu.Unlock()
	for ctr := range sess.containers {
		if ctr != nil {
			ctr := ctr
			releaseGroup.Go(func() error {
				return ctr.Release(ctx)
			})
		}
	}

	for _, client := range sess.clients {
		client := client
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

			return errs
		})
	}
	errs = errors.Join(errs, releaseGroup.Wait())

	// release all the references solved in the session
	sess.refsMu.Lock()
	var refReleaseGroup errgroup.Group
	for rf := range sess.refs {
		if rf != nil {
			rf := rf
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
	telemetry.Flush(ctx)

	return errs
}

type ClientInitOpts struct {
	*engine.ClientMetadata

	// If the client is running from a function in a module, this is the encoded dagQL ID
	// of that module.
	EncodedModuleID string

	// If the client is running from a function in a module, this is the encoded function call
	// metadata (of type core.FunctionCall)
	EncodedFunctionCall json.RawMessage
}

// requires that client.stateMu is held
func (srv *Server) initializeDaggerClient(
	ctx context.Context,
	client *daggerClient,
	failureCleanups *buildkit.Cleanups,
	opts *ClientInitOpts,
) error {
	// initialize all the buildkit state for the client
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

	client.getMainClientCaller = sync.OnceValues(func() (bksession.Caller, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return srv.bkSessionManager.Get(ctx, client.daggerSession.mainClientCallerID, false)
	})

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
		prefix := fmt.Sprintf("[buildkit] [trace=%s] [client=%s] ", client.spanCtx.TraceID(), client.clientID)
		bkLogsW = prefixw.New(bkLogsW, prefix)
		statusCh := make(chan *bkclient.SolveStatus, 8)
		pw, err := progressui.NewDisplay(bkLogsW, progressui.PlainMode)
		if err != nil {
			return fmt.Errorf("failed to create progress writer: %w", err)
		}
		go client.job.Status(ctx, statusCh)
		go pw.UpdateFrom(ctx, statusCh)
	}

	client.bkClient, err = buildkit.NewClient(ctx, &buildkit.Opts{
		Worker:                 srv.worker,
		SessionManager:         srv.bkSessionManager,
		BkSession:              client.buildkitSession,
		Job:                    client.job,
		LLBSolver:              client.llbSolver,
		LLBBridge:              client.llbBridge,
		Dialer:                 client.dialer,
		GetMainClientCaller:    client.getMainClientCaller,
		Entitlements:           srv.entitlements,
		SecretStore:            client.daggerSession.secretStore,
		AuthProvider:           client.daggerSession.authProvider,
		UpstreamCacheImporters: srv.cacheImporters,
		UpstreamCacheImports:   client.daggerSession.cacheImporterCfgs,
		Frontends:              srv.frontends,

		Refs:         client.daggerSession.refs,
		RefsMu:       &client.daggerSession.refsMu,
		Containers:   client.daggerSession.containers,
		ContainersMu: &client.daggerSession.containersMu,

		SpanCtx: client.spanCtx,

		Interactive: client.daggerSession.interactive,
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

	if opts.EncodedModuleID == "" {
		client.deps = core.NewModDeps(client.dagqlRoot, []core.Mod{coreMod})
		clientVersion := client.clientVersion
		if !semver.IsValid(clientVersion) {
			clientVersion = ""
		}
		coreMod.Dag.View = clientVersion
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
		engineVersion, err := client.mod.Source.Self.ModuleEngineVersion(ctx)
		if err != nil {
			return err
		}
		coreMod.Dag.View = engineVersion

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

	client.state = clientStateInitialized
	return nil
}

func (srv *Server) clientFromContext(ctx context.Context) (*daggerClient, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata for session call: %w", err)
	}

	srv.daggerSessionsMu.RLock()
	defer srv.daggerSessionsMu.RUnlock()
	sess, ok := srv.daggerSessions[clientMetadata.SessionID]
	if !ok {
		return nil, fmt.Errorf("session %q not found", clientMetadata.SessionID)
	}

	sess.clientMu.RLock()
	defer sess.clientMu.RUnlock()
	client, ok := sess.clients[clientMetadata.ClientID]
	if !ok {
		return nil, fmt.Errorf("client %q not found", clientMetadata.ClientID)
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
			state:         clientStateUninitialized,
			daggerSession: sess,
			clientID:      clientID,
			clientVersion: opts.ClientVersion,
			secretToken:   token,
		}
		sess.clients[clientID] = client

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

// ServeHTTP serves clients directly hitting the engine API (i.e. main client callers, not nested execs like module functions)
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	clientMetadata, err := engine.ClientMetadataFromHTTPHeaders(r.Header)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get client metadata: %v", err), http.StatusInternalServerError)
		return
	}

	if err := engine.CheckVersionCompatibility(clientMetadata.ClientVersion, engine.MinimumClientVersion); err != nil {
		http.Error(w, fmt.Sprintf("incompatible client version: %s", err), http.StatusInternalServerError)
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
	httpHandlerFunc(srv.serveHTTPToClient, &ClientInitOpts{
		ClientMetadata: &engine.ClientMetadata{
			ClientID:          execMD.ClientID,
			ClientVersion:     engine.Version,
			ClientSecretToken: execMD.SecretToken,
			SessionID:         execMD.SessionID,
			ClientHostname:    execMD.Hostname,
			Labels:            map[string]string{},
		},
		EncodedModuleID:     execMD.EncodedModuleID,
		EncodedFunctionCall: execMD.EncodedFunctionCall,
	}).ServeHTTP(w, r)
}

func (srv *Server) serveHTTPToClient(w http.ResponseWriter, r *http.Request, opts *ClientInitOpts) (rerr error) {
	ctx := r.Context()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	clientMetadata := opts.ClientMetadata
	ctx = engine.ContextWithClientMetadata(ctx, clientMetadata)

	// propagate span context from the client
	ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(r.Header))

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
	}).Debug("handling http request")

	client, cleanup, err := srv.getOrInitClient(ctx, opts)
	if err != nil {
		return fmt.Errorf("update session state: %w", err)
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

	mux := http.NewServeMux()
	mux.Handle(engine.SessionAttachablesEndpoint, httpHandlerFunc(srv.serveSessionAttachables, client))
	mux.Handle(engine.QueryEndpoint, httpHandlerFunc(srv.serveQuery, client))
	mux.Handle(engine.ShutdownEndpoint, httpHandlerFunc(srv.serveShutdown, client))
	sess.endpointMu.RLock()
	for path, handler := range sess.endpoints {
		mux.Handle(path, handler)
	}
	sess.endpointMu.RUnlock()

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

	// get the schema we're gonna serve to this client based on which modules they have loaded, if any
	schema, err := client.deps.Schema(ctx)
	if err != nil {
		return httpErr(fmt.Errorf("failed to get schema: %w", err), http.StatusBadRequest)
	}

	gqlSrv := handler.NewDefaultServer(schema)
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

	gqlSrv.ServeHTTP(w, r)
	return nil
}

func (srv *Server) serveShutdown(w http.ResponseWriter, r *http.Request, client *daggerClient) (rerr error) {
	ctx := r.Context()

	immediate := r.URL.Query().Get("immediate") == "true"

	sess := client.daggerSession
	slog := slog.With(
		"isImmediate", immediate,
		"isMainClient", client.clientID == sess.mainClientCallerID,
		"sessionID", sess.sessionID,
		"clientID", client.clientID,
		"mainClientID", sess.mainClientCallerID)

	slog.Trace("shutting down server")
	defer slog.Trace("done shutting down server")

	if client.clientID == sess.mainClientCallerID {
		// Stop services, since the main client is going away, and we
		// want the client to see them stop.
		sess.services.StopSessionServices(ctx, sess.sessionID)

		// Start draining telemetry
		srv.telemetryPubSub.Drain(sess.mainClientCallerID, immediate)

		if len(sess.cacheExporterCfgs) > 0 {
			bklog.G(ctx).Debugf("running cache export for client %s", client.clientID)
			cacheExporterFuncs := make([]buildkit.ResolveCacheExporterFunc, len(sess.cacheExporterCfgs))
			for i, cacheExportCfg := range sess.cacheExporterCfgs {
				cacheExportCfg := cacheExportCfg
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

		sess.closeShutdownOnce.Do(func() {
			close(sess.shutdownCh)
		})
	}

	telemetry.Flush(ctx)

	return nil
}

// Stitch in the given module to the list being served to the current client
func (srv *Server) ServeModule(ctx context.Context, mod *core.Module) error {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return err
	}

	engineVersion, err := mod.Source.Self.ModuleEngineVersion(ctx)
	if err != nil {
		return err
	}

	client.stateMu.Lock()
	defer client.stateMu.Unlock()

	client.deps = client.deps.Append(mod)
	for _, depMod := range client.deps.Mods {
		if coreMod, ok := depMod.(*schema.CoreMod); ok {
			// this is needed so that when the cli serves a module, that we
			// serve the coreMod schema associated with that module
			coreMod.Dag.View = engineVersion
			break
		}
	}
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
	if client.mod == nil {
		return nil, core.ErrNoCurrentModule
	}

	return client.mod, nil
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
	return client.daggerSession.secretStore, nil
}

// A map of unique IDs for the result of a given cache entry set query, allowing further queries on the result
// to operate on a stable result rather than the live state.
func (srv *Server) EngineCacheEntrySetMap(ctx context.Context) (*sync.Map, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.daggerSession.cacheEntrySetMap, nil
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

type httpError struct {
	error
	code int
}

func httpErr(err error, code int) httpError {
	return httpError{err, code}
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
		bklog.G(r.Context()).WithError(err).Error("failed to serve request")
		// check whether this is a hijacked connection, if so we can't write any http errors to it
		if _, testErr := w.Write(nil); testErr == http.ErrHijacked {
			return
		}

		var httpErr httpError
		if errors.As(err, &httpErr) {
			http.Error(w, httpErr.Error(), httpErr.code)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
