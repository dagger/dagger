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
	"strings"
	"sync"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/containerd/containerd/defaults"
	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	dagintro "github.com/dagger/dagger/dagql/introspection"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/cache"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/tracing"
	"github.com/moby/buildkit/cache/remotecache"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/locker"
	"github.com/opencontainers/go-digest"
	"github.com/sirupsen/logrus"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vito/progrock"
)

type DaggerServer struct {
	serverID string

	// The root for each client, keyed by client ID
	// This is never explicitly deleted from; instead it will just be garbage collected
	// when this server for the session shuts down
	clientRoots      map[string]*core.Query
	clientRootMu     sync.RWMutex
	perClientMu      *locker.Locker
	connectedClients int

	// the http endpoints being served (as a map since APIs like shellEndpoint can add more)
	endpoints  map[string]http.Handler
	endpointMu sync.RWMutex

	dagqlCache   dagql.Cache
	queryOpts    core.QueryOpts
	buildkitOpts *buildkit.Opts

	services *core.Services

	recorder    *progrock.Recorder
	analytics   analytics.Tracker
	progCleanup func() error

	doneCh    chan struct{}
	closeOnce sync.Once

	mainClientCallerID        string
	upstreamCacheExporterCfgs []bkgw.CacheOptionsEntry
	upstreamCacheExporters    map[string]remotecache.ResolveCacheExporterFunc
}

func (e *BuildkitController) newDaggerServer(ctx context.Context, clientMetadata *engine.ClientMetadata) (*DaggerServer, error) {
	s := &DaggerServer{
		serverID: clientMetadata.ServerID,

		clientRoots: map[string]*core.Query{},
		perClientMu: locker.New(),
		endpoints:   map[string]http.Handler{},

		dagqlCache: dagql.NewCache(),

		doneCh: make(chan struct{}, 1),

		services: core.NewServices(),

		mainClientCallerID:     clientMetadata.ClientID,
		upstreamCacheExporters: e.UpstreamCacheExporters,
	}

	labels := clientMetadata.Labels
	labels = append(labels, pipeline.EngineLabel(e.EngineName))
	labels = append(labels, pipeline.LoadServerLabels(engine.Version, runtime.GOOS, runtime.GOARCH, e.cacheManager.ID() != cache.LocalCacheID)...)
	s.analytics = analytics.New(analytics.Config{
		DoNotTrack: clientMetadata.DoNotTrack || analytics.DoNotTrack(),
		Labels:     labels,
		CloudToken: clientMetadata.CloudToken,
	})

	getSessionCtx, getSessionCancel := context.WithTimeout(ctx, 10*time.Second)
	defer getSessionCancel()
	sessionCaller, err := e.SessionManager.Get(getSessionCtx, clientMetadata.BuildkitSessionID(), false)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	clientConn := sessionCaller.Conn()

	// using a new random ID rather than server ID to squash any nefarious attempts to set
	// a server id that has e.g. ../../.. or similar in it
	progSockPath := fmt.Sprintf("/run/dagger/server-progrock-%s.sock", identity.NewID())

	progClient := progrock.NewProgressServiceClient(clientConn)
	progUpdates, err := progClient.WriteUpdates(ctx)
	if err != nil {
		return nil, err
	}

	progWriter, progCleanup, err := buildkit.ProgrockForwarder(progSockPath, progrock.MultiWriter{
		progrock.NewRPCWriter(clientConn, progUpdates),
		buildkit.ProgrockLogrusWriter{},
	})
	if err != nil {
		return nil, err
	}
	s.progCleanup = progCleanup

	progrockLabels := []*progrock.Label{}
	for _, label := range labels {
		progrockLabels = append(progrockLabels, &progrock.Label{
			Name:  label.Name,
			Value: label.Value,
		})
	}
	s.recorder = progrock.NewRecorder(progWriter, progrock.WithLabels(progrockLabels...))

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

	s.buildkitOpts = &buildkit.Opts{
		Worker:                e.worker,
		SessionManager:        e.SessionManager,
		LLBSolver:             e.llbSolver,
		GenericSolver:         e.genericSolver,
		SecretStore:           secretStore,
		AuthProvider:          authProvider,
		PrivilegedExecEnabled: e.privilegedExecEnabled,
		UpstreamCacheImports:  cacheImporterCfgs,
		ProgSockPath:          progSockPath,
		MainClientCaller:      sessionCaller,
		DNSConfig:             e.DNSConfig,
		Frontends:             e.Frontends,
	}

	s.queryOpts = core.QueryOpts{
		DaggerServer:       s,
		ProgrockSocketPath: progSockPath,
		Services:           s.services,
		Platform:           core.Platform(e.worker.Platforms(true)[0]),
		Secrets:            secretStore,
		OCIStore:           e.worker.ContentStore(),
		LeaseManager:       e.worker.LeaseManager(),
		Auth:               authProvider,
		MainClientCallerID: s.mainClientCallerID,
	}

	// register the main client caller
	newRoot, err := s.newRootForClient(ctx, s.mainClientCallerID, nil)
	if err != nil {
		return nil, err
	}
	s.clientRoots[newRoot.ClientID] = newRoot
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

	s.clientRootMu.Lock()
	s.connectedClients++
	s.clientRootMu.Unlock()
	defer func() {
		s.clientRootMu.Lock()
		s.connectedClients--
		s.clientRootMu.Unlock()
	}()

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
			ctx := bklog.WithLogger(context.Background(), bklog.G(ctx))
			ctx = progrock.ToContext(ctx, s.recorder)
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

	root, ok := s.rootFor(clientMetadata.ClientID)
	if !ok {
		errorOut(fmt.Errorf("client call for %s not found", clientMetadata.ClientID), http.StatusInternalServerError)
		return
	}

	rec := progrock.FromContext(ctx)
	if header := r.Header.Get(client.ProgrockParentHeader); header != "" {
		rec = rec.WithParent(header)
	} else if root.ProgrockParent != "" {
		rec = rec.WithParent(root.ProgrockParent)
	}
	ctx = progrock.ToContext(ctx, rec)

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

	srv := handler.NewDefaultServer(root.Dag)
	/* NB: break glass when needed:
	srv.AroundResponses(func(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
		res := next(ctx)
		pl, err := json.Marshal(res)
		slog.Debug("graphql response", "response", string(pl), "error", err)
		return res
	})
	*/
	mux := http.NewServeMux()
	mux.Handle("/query", srv)
	mux.Handle("/shutdown", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		if len(s.upstreamCacheExporterCfgs) > 0 && clientMetadata.ClientID == s.mainClientCallerID {
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
			s.clientRootMu.RLock()
			bk := s.clientRoots[s.mainClientCallerID].Buildkit
			s.clientRootMu.RUnlock()
			err := bk.UpstreamCacheExport(ctx, cacheExporterFuncs)
			if err != nil {
				bklog.G(ctx).WithError(err).Errorf("error running cache export for client %s", clientMetadata.ClientID)
			}
			bklog.G(ctx).Debugf("done running cache export for client %s", clientMetadata.ClientID)
		}
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
	s.clientRootMu.Lock()
	defer s.clientRootMu.Unlock()
	existingRoot, ok := s.clientRoots[clientID]
	if !ok {
		return fmt.Errorf("client ID %q not found", clientID)
	}
	if existingToken := existingRoot.SecretToken; existingToken != "" {
		if existingToken != secretToken {
			return fmt.Errorf("client ID %q already registered with different secret token", clientID)
		}
		return nil
	}
	existingRoot.SecretToken = secretToken
	// NOTE: we purposely don't delete the secret token, it should never be reused and will be released
	// from memory once the dagger server instance corresponding to this buildkit client shuts down.
	// Deleting it would make it easier to create race conditions around using the client's session
	// before it is fully closed.

	return nil
}

func (s *DaggerServer) VerifyClient(clientID, secretToken string) error {
	s.clientRootMu.RLock()
	defer s.clientRootMu.RUnlock()
	existingRoot, ok := s.clientRoots[clientID]
	if !ok {
		return fmt.Errorf("client ID %q not found", clientID)
	}
	if existingRoot.SecretToken != secretToken {
		return fmt.Errorf("client ID %q registered with different secret token", clientID)
	}
	return nil
}

func (s *DaggerServer) MuxEndpoint(ctx context.Context, path string, handler http.Handler) error {
	s.endpointMu.Lock()
	defer s.endpointMu.Unlock()
	s.endpoints[path] = handler
	return nil
}

func (s *DaggerServer) ServeModule(ctx context.Context, mod *core.Module) error {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}

	s.clientRootMu.Lock()
	defer s.clientRootMu.Unlock()
	root, ok := s.clientRoots[clientMetadata.ClientID]
	if !ok {
		return fmt.Errorf("client call for %s not found", clientMetadata.ClientID)
	}
	root.Deps = root.Deps.Append(mod)
	if err := mod.Install(ctx, root.Dag); err != nil {
		return fmt.Errorf("install module: %w", err)
	}
	return nil
}

// Initialize a new root Query for the current dagql call (or return an existing one if it already exists for the current call's digest).
func (s *DaggerServer) NewRootForCurrentCall(ctx context.Context, fnCall *core.FunctionCall) (*core.Query, error) {
	if fnCall == nil {
		fnCall = &core.FunctionCall{}
	}

	clientID := s.digestToClientID(dagql.CurrentID(ctx).Digest(), fnCall.Cache)

	s.perClientMu.Lock(clientID)
	defer s.perClientMu.Unlock(clientID)

	s.clientRootMu.Lock()
	if root, ok := s.clientRoots[clientID]; ok {
		s.clientRootMu.Unlock()
		return root, nil
	}
	s.clientRootMu.Unlock()

	newRoot, err := s.newRootForClient(ctx, clientID, fnCall)
	if err != nil {
		return nil, fmt.Errorf("new root: %w", err)
	}
	s.clientRoots[newRoot.ClientID] = newRoot
	return newRoot, nil
}

// Initialize a new root Query for the given set of dependency mods (or return an existing one if it already exists for the dep's digest).
func (s *DaggerServer) NewRootForDependencies(ctx context.Context, deps *core.ModDeps) (*core.Query, error) {
	clientID := "deps" + s.digestToClientID(deps.Digest(), false)

	s.perClientMu.Lock(clientID)
	defer s.perClientMu.Unlock(clientID)

	s.clientRootMu.Lock()
	if root, ok := s.clientRoots[clientID]; ok {
		s.clientRootMu.Unlock()
		return root, nil
	}
	s.clientRootMu.Unlock()

	newRoot, err := s.newRootForClient(ctx, clientID, nil)
	if err != nil {
		return nil, fmt.Errorf("new root: %w", err)
	}
	if err := deps.Install(ctx, newRoot.Dag); err != nil {
		return nil, fmt.Errorf("install dep: %w", err)
	}
	newRoot.Deps = newRoot.Deps.Append(deps.Mods...)

	s.clientRoots[newRoot.ClientID] = newRoot
	return newRoot, nil
}

// Initialize a new root Query for the given ID based on the modules it needs to be loaded (or return an existing one if it already exists for the ID's digest).
func (s *DaggerServer) NewRootForDynamicID(ctx context.Context, id *call.ID) (*core.Query, error) {
	clientID := "dynamicid" + s.digestToClientID(id.Digest(), false)

	s.perClientMu.Lock(clientID)
	defer s.perClientMu.Unlock(clientID)

	s.clientRootMu.Lock()
	if root, ok := s.clientRoots[clientID]; ok {
		s.clientRootMu.Unlock()
		return root, nil
	}
	s.clientRootMu.Unlock()

	newRoot, err := s.newRootForClient(ctx, clientID, nil)
	if err != nil {
		return nil, fmt.Errorf("new root: %w", err)
	}

	deps := core.NewModDeps(nil)
	for _, modID := range id.Modules() {
		mod, err := dagql.NewID[*core.Module](modID.ID()).Load(ctx, newRoot.Dag)
		if err != nil {
			return nil, fmt.Errorf("load source mod: %w", err)
		}
		deps = deps.Append(mod.Self)
	}
	if err := deps.Install(ctx, newRoot.Dag); err != nil {
		return nil, fmt.Errorf("install deps for dynamic id: %w", err)
	}
	newRoot.Deps = newRoot.Deps.Append(deps.Mods...)

	s.clientRoots[newRoot.ClientID] = newRoot
	return newRoot, nil
}

func (s *DaggerServer) digestToClientID(dgst digest.Digest, cache bool) string {
	clientIDInputs := []string{dgst.String()}
	if !cache {
		// use the ServerID so that we bust cache once-per-session
		clientIDInputs = append(clientIDInputs, s.serverID)
	}
	clientIDDigest := digest.FromString(strings.Join(clientIDInputs, " "))

	// only use encoded part of digest because this ID ends up becoming a buildkit Session ID
	// and buildkit has some ancient internal logic that splits on a colon to support some
	// dev mode logic: https://github.com/moby/buildkit/pull/290
	// also trim it to 25 chars as it ends up becoming part of service URLs
	return clientIDDigest.Encoded()[:25]
}

func (s *DaggerServer) newRootForClient(ctx context.Context, clientID string, fnCall *core.FunctionCall) (*core.Query, error) {
	if fnCall == nil {
		fnCall = &core.FunctionCall{}
	}
	root := &core.Query{
		QueryOpts:      s.queryOpts,
		ClientID:       clientID,
		FnCall:         fnCall,
		ProgrockParent: progrock.FromContext(ctx).Parent,
	}

	var err error
	root.Buildkit, err = buildkit.NewClient(ctx, s.buildkitOpts)
	if err != nil {
		return nil, fmt.Errorf("buildkit client: %w", err)
	}

	// NOTE: context.WithoutCancel is used because if the provided context is canceled, buildkit can
	// leave internal progress contexts open and leak goroutines.
	root.Buildkit.WriteStatusesTo(context.WithoutCancel(ctx), s.recorder)

	root.Dag = dagql.NewServer(root)
	root.Dag.Around(tracing.AroundFunc)
	root.Dag.Cache = s.dagqlCache
	dagintro.Install[*core.Query](root.Dag)

	coreMod := &schema.CoreMod{Dag: root.Dag}
	root.Deps = core.NewModDeps([]core.Mod{coreMod})
	if fnCall.Module != nil {
		root.Deps = root.Deps.Append(fnCall.Module.Deps.Mods...)
		// By default, serve both deps and the module's own API to itself. But if SkipSelfSchema is set,
		// only serve the APIs of the deps of this module. This is currently only needed for the special
		// case of the function used to get the definition of the module itself (which can't obviously
		// be served the API its returning the definition of).
		if !fnCall.SkipSelfSchema {
			root.Deps = root.Deps.Append(fnCall.Module)
		}
	}
	if err := root.Deps.Install(ctx, root.Dag); err != nil {
		return nil, fmt.Errorf("install deps: %w", err)
	}

	return root, nil
}

func (s *DaggerServer) rootFor(clientID string) (*core.Query, bool) {
	s.clientRootMu.RLock()
	defer s.clientRootMu.RUnlock()
	ctx, ok := s.clientRoots[clientID]
	return ctx, ok
}

func (s *DaggerServer) CurrentServedDeps(ctx context.Context) (*core.ModDeps, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	root, ok := s.rootFor(clientMetadata.ClientID)
	if !ok {
		return nil, fmt.Errorf("client call for %s not found", clientMetadata.ClientID)
	}
	return root.Deps, nil
}

func (s *DaggerServer) LogMetrics(l *logrus.Entry) *logrus.Entry {
	s.clientRootMu.RLock()
	defer s.clientRootMu.RUnlock()
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

	s.clientRootMu.RLock()
	for _, root := range s.clientRoots {
		err = errors.Join(err, root.Buildkit.Close())
	}
	s.clientRootMu.RUnlock()

	// mark all groups completed
	s.recorder.Complete()
	// close the recorder so the UI exits
	err = errors.Join(err, s.recorder.Close())
	err = errors.Join(err, s.progCleanup())
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
