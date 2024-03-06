package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"sync"
	"time"

	controlapi "github.com/moby/buildkit/api/services/control"
	apitypes "github.com/moby/buildkit/api/types"
	"github.com/moby/buildkit/cache/remotecache"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/executor/oci"
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
	bkworker "github.com/moby/buildkit/worker"
	"github.com/moby/locker"
	"github.com/sirupsen/logrus"
	logsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/telemetry"
	"github.com/dagger/dagger/tracing"
)

type BuildkitController struct {
	BuildkitControllerOpts

	llbSolver             *llbsolver.Solver
	genericSolver         *solver.Solver
	cacheManager          solver.CacheManager
	worker                bkworker.Worker
	privilegedExecEnabled bool

	// server id -> server
	servers     map[string]*DaggerServer
	serverMu    sync.RWMutex
	perServerMu *locker.Locker

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
	TelemetryPubSub        *tracing.PubSub
	UpstreamCacheExporters map[string]remotecache.ResolveCacheExporterFunc
	UpstreamCacheImporters map[string]remotecache.ResolveCacheImporterFunc
	DNSConfig              *oci.DNSConfig
}

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
		perServerMu:            locker.New(),
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

func (e *BuildkitController) LogMetrics(l *logrus.Entry) *logrus.Entry {
	e.serverMu.RLock()
	defer e.serverMu.RUnlock()
	l = l.WithField("dagger-server-count", len(e.servers))
	for _, s := range e.servers {
		l = s.LogMetrics(l)
	}
	return l
}

func (e *BuildkitController) Session(stream controlapi.Control_SessionServer) (rerr error) {
	defer func() {
		// a panic would indicate a bug, but we don't want to take down the entire server
		if err := recover(); err != nil {
			bklog.G(context.Background()).WithError(fmt.Errorf("%v", err)).Errorf("panic in session call")
			debug.PrintStack()
			rerr = fmt.Errorf("panic in session call, please report a bug: %v %s", err, string(debug.Stack()))
		}
	}()

	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	opts, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		bklog.G(ctx).WithError(err).Errorf("failed to get client metadata for session call")
		return fmt.Errorf("failed to get client metadata for session call: %w", err)
	}
	ctx = bklog.WithLogger(ctx, bklog.G(ctx).
		WithField("client_id", opts.ClientID).
		WithField("client_hostname", opts.ClientHostname).
		WithField("client_call_digest", opts.ModuleCallerDigest).
		WithField("server_id", opts.ServerID))
	bklog.G(ctx).WithField("register_client", opts.RegisterClient).Debug("handling session call")
	defer func() {
		if rerr != nil {
			bklog.G(ctx).WithError(rerr).Errorf("session call failed")
		} else {
			bklog.G(ctx).Debugf("session call done")
		}
	}()

	conn, _, hijackmd := grpchijack.Hijack(stream)

	if !opts.RegisterClient {
		e.serverMu.RLock()
		srv, ok := e.servers[opts.ServerID]
		e.serverMu.RUnlock()
		if !ok {
			return fmt.Errorf("server %q not found", opts.ServerID)
		}
		bklog.G(ctx).Debugf("forwarding client to server")
		err = srv.ServeClientConn(ctx, opts, conn)
		if errors.Is(err, io.ErrClosedPipe) {
			return nil
		}
		return fmt.Errorf("serve clientConn: %w", err)
	}

	bklog.G(ctx).Debugf("registering client")

	eg, egctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		bklog.G(ctx).Debug("session manager handling conn")
		err := e.SessionManager.HandleConn(egctx, conn, hijackmd)
		bklog.G(ctx).WithError(err).Debug("session manager handle conn done")
		if err != nil {
			return fmt.Errorf("handleConn: %w", err)
		}
		return nil
	})

	// NOTE: the perServerMu here is used to ensure that we hold a lock
	// specific to only *this server*, so we don't allow creating multiple
	// servers with the same ID at once. This complexity is necessary so we
	// don't hold the global serverMu lock for longer than necessary.
	e.perServerMu.Lock(opts.ServerID)
	e.serverMu.RLock()
	srv, ok := e.servers[opts.ServerID]
	e.serverMu.RUnlock()
	if !ok {
		bklog.G(ctx).Debugf("initializing new server")

		srv, err = e.newDaggerServer(ctx, opts)
		if err != nil {
			e.perServerMu.Unlock(opts.ServerID)
			return fmt.Errorf("new APIServer: %w", err)
		}
		e.serverMu.Lock()
		e.servers[opts.ServerID] = srv
		e.serverMu.Unlock()

		bklog.G(ctx).Debugf("initialized new server")

		// delete the server after the initial client who created it exits
		defer func() {
			bklog.G(ctx).Debug("removing server")
			e.serverMu.Lock()
			delete(e.servers, opts.ServerID)
			e.serverMu.Unlock()

			if err := srv.Close(context.WithoutCancel(ctx)); err != nil {
				bklog.G(ctx).WithError(err).Error("failed to close server")
			}

			time.AfterFunc(time.Second, e.throttledGC)
			bklog.G(ctx).Debug("server removed")
		}()
	}
	e.perServerMu.Unlock(opts.ServerID)

	err = srv.RegisterClient(opts.ClientID, opts.ClientHostname, opts.ClientSecretToken)
	if err != nil {
		return fmt.Errorf("failed to register client: %w", err)
	}

	eg.Go(func() error {
		bklog.G(ctx).Trace("waiting for server")
		err := srv.Wait(egctx)
		bklog.G(ctx).WithError(err).Trace("server done")
		if err != nil {
			return fmt.Errorf("srv.Wait: %w", err)
		}
		return nil
	})
	err = eg.Wait()
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	if err != nil {
		return fmt.Errorf("wait: %w", err)
	}
	return nil
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

	e.serverMu.RLock()
	cancelLeases := len(e.servers) == 0
	e.serverMu.RUnlock()
	if cancelLeases {
		imageutil.CancelCacheLeases()
	}

	didPrune := false
	defer func() {
		if didPrune {
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
			Package:  engine.Package,
			Version:  engine.Version,
			Revision: e.EngineName,
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
	tracev1.RegisterTraceServiceServer(server, &telemetry.TraceServer{Exporter: e.TelemetryPubSub})
	logsv1.RegisterLogsServiceServer(server, &telemetry.LogsServer{Exporter: e.TelemetryPubSub})
}

func (e *BuildkitController) Close() error {
	err := e.WorkerController.Close()

	// note this *could* cause a panic in Session if it was still running, so
	// the server should be shutdown first
	e.serverMu.Lock()
	servers := e.servers
	e.servers = nil
	e.serverMu.Unlock()

	for _, s := range servers {
		s.Close(context.Background())
	}
	return err
}

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

func (e *BuildkitController) Solve(ctx context.Context, req *controlapi.SolveRequest) (*controlapi.SolveResponse, error) {
	return nil, fmt.Errorf("solve not implemented")
}

func (e *BuildkitController) Status(req *controlapi.StatusRequest, stream controlapi.Control_StatusServer) error {
	return fmt.Errorf("status not implemented")
}

func (e *BuildkitController) ListenBuildHistory(req *controlapi.BuildHistoryRequest, srv controlapi.Control_ListenBuildHistoryServer) error {
	return fmt.Errorf("listen build history not implemented")
}

func (e *BuildkitController) UpdateBuildHistory(ctx context.Context, req *controlapi.UpdateBuildHistoryRequest) (*controlapi.UpdateBuildHistoryResponse, error) {
	return nil, fmt.Errorf("update build history not implemented")
}
