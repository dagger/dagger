package server

import (
	"context"
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
	"github.com/moby/buildkit/worker"
	bkworker "github.com/moby/buildkit/worker"
	tracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

/* TODO: renaming idea:
* Server becomes EngineServer
* Router becomes DaggerAPIServer or maybe SessionServer?
 */

// TODO: just hit a panic in Server.Serve but client just sat blocked, fix that
type Server struct {
	ServerOpts
	llbSolver             *llbsolver.Solver
	genericSolver         *solver.Solver
	cacheManager          solver.CacheManager
	worker                bkworker.Worker
	privilegedExecEnabled bool

	// router id -> router
	routers  map[string]*Router
	routerMu sync.Mutex

	throttledGC func()
	gcmu        sync.Mutex
}

// TODO: make your own Opt
type ServerOpts struct {
	WorkerController       *worker.Controller
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
func NewServer(opts ServerOpts) (*Server, error) {
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

	e := &Server{
		ServerOpts:    opts,
		llbSolver:     llbSolver,
		genericSolver: genericSolver,
		cacheManager:  opts.CacheManager,
		worker:        w,
		routers:       make(map[string]*Router),
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

func (e *Server) Solve(ctx context.Context, req *controlapi.SolveRequest) (*controlapi.SolveResponse, error) {
	opts, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	getCallerCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	caller, err := e.SessionManager.Get(getCallerCtx, opts.ClientID, false)
	if err != nil {
		e.routerMu.Unlock()
		return nil, err
	}

	e.routerMu.Lock()
	rtr, ok := e.routers[opts.RouterID]
	if !ok {
		secretStore := core.NewSecretStore()
		authProvider := auth.NewRegistryAuthProvider()

		bkClient, err := buildkit.NewClient(ctx, buildkit.Opts{
			Worker:                e.worker,
			SessionManager:        e.SessionManager,
			LLBSolver:             e.llbSolver,
			GenericSolver:         e.genericSolver,
			SecretStore:           secretStore,
			AuthProvider:          authProvider,
			PrivilegedExecEnabled: e.privilegedExecEnabled,
			MainClientCaller:      caller,
			Metadata:              opts,
		})
		if err != nil {
			e.routerMu.Unlock()
			return nil, err
		}
		secretStore.SetBuildkitClient(bkClient)

		labels := opts.Labels
		labels = append(labels, pipeline.EngineLabel(e.EngineName))
		rtr, err = NewRouter(ctx, bkClient, e.worker, caller, opts.RouterID, secretStore, authProvider, labels, opts.ParentClientIDs)
		if err != nil {
			e.routerMu.Unlock()
			return nil, err
		}
		e.routers[opts.RouterID] = rtr

		// delete the router after the initial client who created it exits
		defer func() {
			e.routerMu.Lock()
			rtr.Close()
			delete(e.routers, opts.RouterID)
			e.routerMu.Unlock()

			if err := bkClient.Close(); err != nil {
				bklog.G(ctx).WithError(err).Errorf("failed to close buildkit client for router %s", opts.RouterID)
			}

			// TODO: synchronous? or put this in a goroutine?
			time.AfterFunc(time.Second, e.throttledGC)
		}()
	}
	e.routerMu.Unlock()
	rtr.bkClient.RegisterClient(opts.ClientID, opts.ClientHostname)
	defer rtr.bkClient.DeregisterClientHostname(opts.ClientHostname)

	// TODO: re-add support for upstream cache import/export

	err = rtr.Wait(ctx)
	if err != nil {
		return nil, err
	}

	return &controlapi.SolveResponse{}, nil
}

func (e *Server) Session(stream controlapi.Control_SessionServer) (rerr error) {
	lg := bklog.G(stream.Context())

	// TODO: should think through case where evil client lies about its ID
	// maybe maintain a map of secret session token -> client ID and verify against that or something
	// Also, may be good idea to have one secret token per client rather than shared across multiple
	opts, err := engine.SessionAPIOptsFromContext(stream.Context())
	if err != nil {
		lg.WithError(err).Error("failed to get session api opts")
		return err
	}

	lg = lg.
		WithField("client_id", opts.ClientID).
		WithField("client_hostname", opts.ClientHostname).
		WithField("router_id", opts.RouterID).
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
	} else {
		// TODO:
		lg.Debug("passing through to graphql api")

		e.routerMu.Lock()
		rtr, ok := e.routers[opts.RouterID]
		if !ok {
			e.routerMu.Unlock()
			return fmt.Errorf("router %q not found", opts.RouterID)
		}
		e.routerMu.Unlock()

		// default to connecting to the graphql api
		// TODO: make sure this unblocks and has reasonable error if router is closed, I don't think either are true rn
		err = rtr.ServeClientConn(ctx, opts.ClientMetadata, conn)
	}

	return err
}

func (e *Server) DiskUsage(ctx context.Context, r *controlapi.DiskUsageRequest) (*controlapi.DiskUsageResponse, error) {
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

func (e *Server) Prune(req *controlapi.PruneRequest, stream controlapi.Control_PruneServer) error {
	eg, ctx := errgroup.WithContext(stream.Context())

	e.routerMu.Lock()
	if len(e.routers) == 0 {
		e.routerMu.Unlock()
		imageutil.CancelCacheLeases()
	}
	e.routerMu.Unlock()

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

func (e *Server) Info(ctx context.Context, r *controlapi.InfoRequest) (*controlapi.InfoResponse, error) {
	return &controlapi.InfoResponse{
		BuildkitVersion: &apitypes.BuildkitVersion{
			Version: engine.Version,
		},
	}, nil
}

func (e *Server) ListWorkers(ctx context.Context, r *controlapi.ListWorkersRequest) (*controlapi.ListWorkersResponse, error) {
	resp := &controlapi.ListWorkersResponse{
		Record: []*apitypes.WorkerRecord{{
			ID:        e.worker.ID(),
			Labels:    e.worker.Labels(),
			Platforms: pb.PlatformsFromSpec(e.worker.Platforms(true)),
		}},
	}
	return resp, nil
}

func (e *Server) Register(server *grpc.Server) {
	controlapi.RegisterControlServer(server, e)
	// TODO: needed?
	// tracev1.RegisterTraceServiceServer(server, e)
	// e.gatewayForwarder.Register(server)
}

func (e *Server) Close() error {
	// TODO: ensure all routers are closed
	return e.WorkerController.Close()
}

// TODO: just inline this so we have less fields?
func (e *Server) gc() {
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

func (e *Server) Status(req *controlapi.StatusRequest, stream controlapi.Control_StatusServer) error {
	// we send status updates over progrock session attachables instead
	return fmt.Errorf("status not implemented")
}

func (e *Server) Export(ctx context.Context, req *tracev1.ExportTraceServiceRequest) (*tracev1.ExportTraceServiceResponse, error) {
	// TODO: not sure if we ever use this
	return nil, fmt.Errorf("export not implemented")
}

func (e *Server) ListenBuildHistory(req *controlapi.BuildHistoryRequest, srv controlapi.Control_ListenBuildHistoryServer) error {
	return fmt.Errorf("listen build history not implemented")
}

func (e *Server) UpdateBuildHistory(ctx context.Context, req *controlapi.UpdateBuildHistoryRequest) (*controlapi.UpdateBuildHistoryResponse, error) {
	return nil, fmt.Errorf("update build history not implemented")
}
