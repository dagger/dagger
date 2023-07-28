package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	controlapi "github.com/moby/buildkit/api/services/control"
	apitypes "github.com/moby/buildkit/api/types"
	"github.com/moby/buildkit/cache/remotecache"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/frontend"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/grpchijack"
	containerdsnapshot "github.com/moby/buildkit/snapshot/containerd"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/llbsolver"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/entitlements"
	"github.com/moby/buildkit/util/imageutil"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/moby/buildkit/util/throttle"
	bkworker "github.com/moby/buildkit/worker"
	tracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

// TODO: just hit a panic in BuildkitController.Serve but client just sat blocked, fix that
type BuildkitController struct {
	BuildkitControllerOpts
	llbSolver             *llbsolver.Solver
	genericSolver         *solver.Solver
	cacheManager          solver.CacheManager
	worker                bkworker.Worker
	privilegedExecEnabled bool

	// server id -> server
	servers  map[string]*DaggerServer
	serverMu sync.Mutex

	throttledGC func()
	gcmu        sync.Mutex
}

type BuildkitControllerOpts struct {
	WorkerController       *bkworker.Controller
	SessionManager         *session.Manager
	CacheManager           solver.CacheManager
	ContentStore           *containerdsnapshot.Store
	LeaseManager           *leaseutil.Manager
	Entitlements           []string
	EngineName             string
	Frontends              map[string]frontend.Frontend
	UpstreamCacheExporters map[string]remotecache.ResolveCacheExporterFunc
	UpstreamCacheImporters map[string]remotecache.ResolveCacheImporterFunc
}

// TODO: setup cache manager here instead of cmd/engine/main.go
func NewBuildkitController(opts BuildkitControllerOpts) (*BuildkitController, error) {
	w, err := opts.WorkerController.GetDefault()
	if err != nil {
		return nil, fmt.Errorf("failed to get default worker: %w", err)
	}

	llbSolver, err := llbsolver.New(llbsolver.Opt{
		WorkerController: opts.WorkerController,
		Frontends:        opts.Frontends,
		CacheManager:     opts.CacheManager,
		SessionManager:   opts.SessionManager,
		CacheResolvers:   opts.UpstreamCacheImporters,
		Entitlements:     opts.Entitlements,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create solver: %w", err)
	}

	genericSolver := solver.NewSolver(solver.SolverOpt{
		ResolveOpFunc: func(vtx solver.Vertex, builder solver.Builder) (solver.Op, error) {
			return w.ResolveOp(vtx, llbSolver.Bridge(builder), opts.SessionManager)
		},
		DefaultCache: opts.CacheManager,
	})

	e := &BuildkitController{
		BuildkitControllerOpts: opts,
		llbSolver:              llbSolver,
		genericSolver:          genericSolver,
		cacheManager:           opts.CacheManager,
		worker:                 w,
		servers:                make(map[string]*DaggerServer),
	}

	for _, entitlementStr := range opts.Entitlements {
		if entitlementStr == string(entitlements.EntitlementSecurityInsecure) {
			e.privilegedExecEnabled = true
		}
	}

	e.throttledGC = throttle.After(time.Minute, e.gc)
	defer func() {
		time.AfterFunc(time.Second, e.throttledGC)
	}()

	return e, nil
}

func (e *BuildkitController) Solve(ctx context.Context, req *controlapi.SolveRequest) (*controlapi.SolveResponse, error) {
	opts, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	getCallerCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	caller, err := e.SessionManager.Get(getCallerCtx, opts.ClientID, false)
	if err != nil {
		return nil, err
	}

	e.serverMu.Lock()
	rtr, ok := e.servers[opts.ServerID]
	if !ok {
		bklog.G(ctx).Debugf("creating new server %q for client %s", opts.ServerID, opts.ClientID)
		secretStore := core.NewSecretStore()
		authProvider := auth.NewRegistryAuthProvider()

		var cacheImporterCfgs []bkgw.CacheOptionsEntry
		for _, cacheImportCfg := range req.Cache.Imports {
			_, ok := e.UpstreamCacheImporters[cacheImportCfg.Type]
			if !ok {
				e.serverMu.Unlock()
				return nil, fmt.Errorf("unknown cache importer type %q", cacheImportCfg.Type)
			}
			cacheImporterCfgs = append(cacheImporterCfgs, bkgw.CacheOptionsEntry{
				Type:  cacheImportCfg.Type,
				Attrs: cacheImportCfg.Attrs,
			})
		}

		bkClient, err := buildkit.NewClient(ctx, buildkit.Opts{
			Worker:                e.worker,
			SessionManager:        e.SessionManager,
			LLBSolver:             e.llbSolver,
			GenericSolver:         e.genericSolver,
			SecretStore:           secretStore,
			AuthProvider:          authProvider,
			PrivilegedExecEnabled: e.privilegedExecEnabled,
			UpstreamCacheImports:  cacheImporterCfgs,
			MainClientCaller:      caller,
		})
		if err != nil {
			e.serverMu.Unlock()
			return nil, err
		}
		secretStore.SetBuildkitClient(bkClient)

		labels := opts.Labels
		labels = append(labels, pipeline.EngineLabel(e.EngineName))
		rtr, err = NewDaggerServer(ctx, bkClient, e.worker, caller, opts.ServerID, secretStore, authProvider, labels)
		if err != nil {
			e.serverMu.Unlock()
			return nil, err
		}
		e.servers[opts.ServerID] = rtr

		// delete the server after the initial client who created it exits
		defer func() {
			e.serverMu.Lock()
			rtr.Close()
			delete(e.servers, opts.ServerID)
			e.serverMu.Unlock()

			if err := bkClient.Close(); err != nil {
				bklog.G(ctx).WithError(err).Errorf("failed to close buildkit client for server %s", opts.ServerID)
			}

			// TODO: synchronous? or put this in a goroutine?
			time.AfterFunc(time.Second, e.throttledGC)
		}()
	}
	e.serverMu.Unlock()

	cacheExporterFuncs := make([]buildkit.ResolveCacheExporterFunc, len(req.Cache.Exports))
	for i, cacheExportCfg := range req.Cache.Exports {
		exporterFunc, ok := e.UpstreamCacheExporters[cacheExportCfg.Type]
		if !ok {
			return nil, fmt.Errorf("unknown cache exporter type %q", cacheExportCfg.Type)
		}
		cacheExporterFuncs[i] = func(ctx context.Context, sessionGroup session.Group) (remotecache.Exporter, error) {
			return exporterFunc(ctx, sessionGroup, cacheExportCfg.Attrs)
		}
	}
	if len(cacheExporterFuncs) > 0 {
		// run cache export instead
		bklog.G(ctx).Debugf("running cache export for client %s", opts.ClientID)
		err := rtr.bkClient.UpstreamCacheExport(ctx, cacheExporterFuncs)
		if err != nil {
			bklog.G(ctx).WithError(err).Errorf("error running cache export for client %s", opts.ClientID)
			return &controlapi.SolveResponse{}, err
		}
		bklog.G(ctx).Debugf("done running cache export for client %s", opts.ClientID)
		return &controlapi.SolveResponse{}, nil
	}

	rtr.bkClient.RegisterClient(opts.ClientID, opts.ClientHostname)
	defer rtr.bkClient.DeregisterClientHostname(opts.ClientHostname)
	err = rtr.Wait(ctx)
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return &controlapi.SolveResponse{}, nil
}

func (e *BuildkitController) Session(stream controlapi.Control_SessionServer) (rerr error) {
	lg := bklog.G(stream.Context())

	// TODO: should think through case where evil client lies about its ID
	// maybe maintain a map of secret session token -> client ID and verify against that or something
	// Also, may be good idea to have one secret token per client rather than shared across multiple
	opts, err := engine.SessionAPIOptsFromContext(stream.Context())
	if err != nil {
		lg.WithError(err).Error("failed to get session api opts")
		return fmt.Errorf("failed to get session api opts: %w", err)
	}

	lg = lg.
		WithField("client_id", opts.ClientID).
		WithField("client_hostname", opts.ClientHostname).
		WithField("server_id", opts.ServerID).
		WithField("buildkit_attachable", opts.BuildkitAttachable)
	lg.Debug("session starting")
	defer lg.WithError(rerr).Debug("session finished")

	conn, closeCh, md := grpchijack.Hijack(stream)
	defer conn.Close()

	// TODO:
	lg.Debug("session hijacked")

	ctx, cancel := context.WithCancel(stream.Context())
	go func() {
		<-closeCh
		cancel()
	}()

	if opts.BuildkitAttachable {
		// TODO:
		lg.Debug("passing through to buildkit session manager")
		// pass through to buildkit's session manager for handling the attachables
		err = e.SessionManager.HandleConn(ctx, conn, md)
		if err != nil {
			err = fmt.Errorf("session manager failed to handle conn: %w", err)
		}
	} else {
		// TODO:
		lg.Debug("passing through to graphql api")

		e.serverMu.Lock()
		rtr, ok := e.servers[opts.ServerID]
		if !ok {
			e.serverMu.Unlock()
			return fmt.Errorf("server %q not found", opts.ServerID)
		}
		e.serverMu.Unlock()

		// default to connecting to the graphql api
		// TODO: make sure this unblocks and has reasonable error if server is closed, I don't think either are true rn
		err = rtr.ServeClientConn(ctx, opts.ClientMetadata, conn)
		if err != nil {
			err = fmt.Errorf("server failed to serve client conn: %w", err)
		}
	}

	return err
}

func (e *BuildkitController) DiskUsage(ctx context.Context, r *controlapi.DiskUsageRequest) (*controlapi.DiskUsageResponse, error) {
	resp := &controlapi.DiskUsageResponse{}
	du, err := e.worker.DiskUsage(ctx, bkclient.DiskUsageInfo{
		Filter: r.Filter,
	})
	if err != nil {
		return nil, err
	}
	for _, r := range du {
		resp.Record = append(resp.Record, &controlapi.UsageRecord{
			ID:          r.ID,
			Mutable:     r.Mutable,
			InUse:       r.InUse,
			Size_:       r.Size,
			Parents:     r.Parents,
			UsageCount:  int64(r.UsageCount),
			Description: r.Description,
			CreatedAt:   r.CreatedAt,
			LastUsedAt:  r.LastUsedAt,
			RecordType:  string(r.RecordType),
			Shared:      r.Shared,
		})
	}
	return resp, nil
}

func (e *BuildkitController) Prune(req *controlapi.PruneRequest, stream controlapi.Control_PruneServer) error {
	eg, ctx := errgroup.WithContext(stream.Context())

	e.serverMu.Lock()
	if len(e.servers) == 0 {
		e.serverMu.Unlock()
		imageutil.CancelCacheLeases()
	}
	e.serverMu.Unlock()

	didPrune := false
	defer func() {
		if didPrune {
			// TODO: we could fix this one off interface definition now...
			if e, ok := e.cacheManager.(interface {
				ReleaseUnreferenced(context.Context) error
			}); ok {
				if err := e.ReleaseUnreferenced(ctx); err != nil {
					bklog.G(ctx).Errorf("failed to release cache metadata: %+v", err)
				}
			}
		}
	}()

	ch := make(chan bkclient.UsageInfo, 32)

	eg.Go(func() error {
		defer close(ch)
		return e.worker.Prune(ctx, ch, bkclient.PruneInfo{
			Filter:       req.Filter,
			All:          req.All,
			KeepDuration: time.Duration(req.KeepDuration),
			KeepBytes:    req.KeepBytes,
		})
	})

	eg.Go(func() error {
		defer func() {
			// drain channel on error
			for range ch {
			}
		}()
		for r := range ch {
			didPrune = true
			if err := stream.Send(&controlapi.UsageRecord{
				ID:          r.ID,
				Mutable:     r.Mutable,
				InUse:       r.InUse,
				Size_:       r.Size,
				Parents:     r.Parents,
				UsageCount:  int64(r.UsageCount),
				Description: r.Description,
				CreatedAt:   r.CreatedAt,
				LastUsedAt:  r.LastUsedAt,
				RecordType:  string(r.RecordType),
				Shared:      r.Shared,
			}); err != nil {
				return err
			}
		}
		return nil
	})

	return eg.Wait()
}

func (e *BuildkitController) Info(ctx context.Context, r *controlapi.InfoRequest) (*controlapi.InfoResponse, error) {
	return &controlapi.InfoResponse{
		BuildkitVersion: &apitypes.BuildkitVersion{
			Package: e.EngineName,
			Version: engine.Version,
		},
	}, nil
}

func (e *BuildkitController) ListWorkers(ctx context.Context, r *controlapi.ListWorkersRequest) (*controlapi.ListWorkersResponse, error) {
	resp := &controlapi.ListWorkersResponse{
		Record: []*apitypes.WorkerRecord{{
			ID:        e.worker.ID(),
			Labels:    e.worker.Labels(),
			Platforms: pb.PlatformsFromSpec(e.worker.Platforms(true)),
		}},
	}
	return resp, nil
}

func (e *BuildkitController) Register(server *grpc.Server) {
	controlapi.RegisterControlServer(server, e)
	// TODO: needed?
	// tracev1.RegisterTraceServiceServer(server, e)
	// e.gatewayForwarder.Register(server)
}

func (e *BuildkitController) Close() error {
	// TODO: ensure all servers are closed
	return e.WorkerController.Close()
}

// TODO: just inline this so we have less fields?
func (e *BuildkitController) gc() {
	e.gcmu.Lock()
	defer e.gcmu.Unlock()

	ch := make(chan bkclient.UsageInfo)
	eg, ctx := errgroup.WithContext(context.TODO())

	var size int64
	eg.Go(func() error {
		for ui := range ch {
			size += ui.Size
		}
		return nil
	})

	eg.Go(func() error {
		defer close(ch)
		if policy := e.worker.GCPolicy(); len(policy) > 0 {
			return e.worker.Prune(ctx, ch, policy...)
		}
		return nil
	})

	err := eg.Wait()
	if err != nil {
		bklog.G(ctx).Errorf("gc error: %+v", err)
	}
	if size > 0 {
		bklog.G(ctx).Debugf("gc cleaned up %d bytes", size)
	}
}

func (e *BuildkitController) Status(req *controlapi.StatusRequest, stream controlapi.Control_StatusServer) error {
	// we send status updates over progrock session attachables instead
	return fmt.Errorf("status not implemented")
}

func (e *BuildkitController) Export(ctx context.Context, req *tracev1.ExportTraceServiceRequest) (*tracev1.ExportTraceServiceResponse, error) {
	// TODO: not sure if we ever use this
	return nil, fmt.Errorf("export not implemented")
}

func (e *BuildkitController) ListenBuildHistory(req *controlapi.BuildHistoryRequest, srv controlapi.Control_ListenBuildHistoryServer) error {
	return fmt.Errorf("listen build history not implemented")
}

func (e *BuildkitController) UpdateBuildHistory(ctx context.Context, req *controlapi.UpdateBuildHistoryRequest) (*controlapi.UpdateBuildHistoryResponse, error) {
	return nil, fmt.Errorf("update build history not implemented")
}
