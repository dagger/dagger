package server

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/diff/apply"
	"github.com/containerd/containerd/diff/walking"
	ctdmetadata "github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes/docker"
	ctdsnapshot "github.com/containerd/containerd/snapshots"
	"github.com/containerd/go-runc"
	controlapi "github.com/moby/buildkit/api/services/control"
	apitypes "github.com/moby/buildkit/api/types"
	bkcache "github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/cache/metadata"
	"github.com/moby/buildkit/cache/remotecache"
	"github.com/moby/buildkit/cache/remotecache/azblob"
	"github.com/moby/buildkit/cache/remotecache/gha"
	inlineremotecache "github.com/moby/buildkit/cache/remotecache/inline"
	localremotecache "github.com/moby/buildkit/cache/remotecache/local"
	registryremotecache "github.com/moby/buildkit/cache/remotecache/registry"
	s3remotecache "github.com/moby/buildkit/cache/remotecache/s3"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/frontend"
	dockerfile "github.com/moby/buildkit/frontend/dockerfile/builder"
	"github.com/moby/buildkit/frontend/gateway"
	"github.com/moby/buildkit/frontend/gateway/forwarder"
	bksession "github.com/moby/buildkit/session"
	containerdsnapshot "github.com/moby/buildkit/snapshot/containerd"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/bboltcachestorage"
	"github.com/moby/buildkit/solver/llbsolver/mounts"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/source"
	srcgit "github.com/moby/buildkit/source/git"
	srchttp "github.com/moby/buildkit/source/http"
	"github.com/moby/buildkit/util/archutil"
	"github.com/moby/buildkit/util/entitlements"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/moby/buildkit/util/network"
	"github.com/moby/buildkit/util/network/cniprovider"
	"github.com/moby/buildkit/util/network/netproviders"
	"github.com/moby/buildkit/util/resolver"
	"github.com/moby/buildkit/util/throttle"
	"github.com/moby/buildkit/util/winlayers"
	"github.com/moby/buildkit/version"
	bkworker "github.com/moby/buildkit/worker"
	"github.com/moby/buildkit/worker/base"
	wlabel "github.com/moby/buildkit/worker/label"
	"github.com/moby/locker"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	bolt "go.etcd.io/bbolt"
	logsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	daggercache "github.com/dagger/dagger/engine/cache"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/sources/blob"
	"github.com/dagger/dagger/engine/sources/gitdns"
	"github.com/dagger/dagger/engine/sources/httpdns"
	enginetel "github.com/dagger/dagger/engine/telemetry"
)

const (
	daggerCacheServiceURL = "https://api.dagger.cloud/magicache"
)

type Engine struct {
	engineName string

	//
	// state directory/db paths
	//

	rootDir           string
	solverCacheDBPath string

	workerRootDir         string
	snapshotterRootDir    string
	contentStoreRootDir   string
	containerdMetaDBPath  string
	workerCacheMetaDBPath string
	buildkitMountPoolDir  string
	executorRootDir       string

	//
	// buildkit+containerd entities/DBs
	//

	baseWorker          *base.Worker
	worker              *buildkit.Worker
	workerCacheMetaDB   *metadata.Store
	workerCache         bkcache.Manager
	workerSourceManager *source.Manager

	bkSessionManager *bksession.Manager

	solver        *solver.Solver
	solverCacheDB *bboltcachestorage.Store
	SolverCache   daggercache.Manager

	containerdMetaBoltDB *bolt.DB
	containerdMetaDB     *ctdmetadata.DB
	localContentStore    content.Store
	contentStore         *containerdsnapshot.Store

	snapshotter     ctdsnapshot.Snapshotter
	snapshotterName string
	leaseManager    *leaseutil.Manager

	frontends map[string]frontend.Frontend

	cacheExporters map[string]remotecache.ResolveCacheExporterFunc
	cacheImporters map[string]remotecache.ResolveCacheImporterFunc

	//
	// worker/executor-specific config+state
	//

	runc             *runc.Runc
	cgroupParent     string
	networkProviders map[pb.NetMode]network.Provider
	processMode      oci.ProcessMode
	dns              *oci.DNSConfig
	apparmorProfile  string
	selinux          bool
	entitlements     entitlements.Set
	parallelismSem   *semaphore.Weighted
	enabledPlatforms []ocispecs.Platform
	registryHosts    docker.RegistryHosts

	//
	// telemetry config+state
	//

	telemetryPubSub *enginetel.PubSub
	buildkitLogSink io.Writer

	//
	// gc related
	//
	throttledGC func()
	gcmu        sync.Mutex

	//
	// session+client state
	//
	servers     map[string]*DaggerServer
	serverMu    sync.RWMutex
	perServerMu *locker.Locker
}

type NewEngineOpts struct {
	EngineConfig *config.Config
	EngineName   string

	TelemetryPubSub *enginetel.PubSub
}

func NewEngine(ctx context.Context, opts *NewEngineOpts) (*Engine, error) {
	cfg := opts.EngineConfig
	ociCfg := cfg.Workers.OCI

	eng := &Engine{
		engineName: opts.EngineName,

		rootDir: cfg.Root,

		frontends: map[string]frontend.Frontend{},

		cgroupParent:    ociCfg.DefaultCgroupParent,
		processMode:     oci.ProcessSandbox,
		apparmorProfile: ociCfg.ApparmorProfile,
		selinux:         ociCfg.SELinux,
		entitlements:    entitlements.Set{},
		dns: &oci.DNSConfig{
			Nameservers:   cfg.DNS.Nameservers,
			Options:       cfg.DNS.Options,
			SearchDomains: cfg.DNS.SearchDomains,
		},

		telemetryPubSub: opts.TelemetryPubSub,

		servers:     make(map[string]*DaggerServer),
		perServerMu: locker.New(),
	}

	//
	// setup directories and paths
	//

	var err error
	eng.rootDir, err = filepath.Abs(eng.rootDir)
	if err != nil {
		return nil, err
	}
	eng.rootDir, err = filepath.EvalSymlinks(eng.rootDir)
	if err != nil {
		return nil, err
	}
	eng.solverCacheDBPath = filepath.Join(eng.rootDir, "cache.db")

	eng.workerRootDir = filepath.Join(eng.rootDir, "worker")
	if err := os.MkdirAll(eng.workerRootDir, 0700); err != nil {
		return nil, err
	}
	eng.snapshotterRootDir = filepath.Join(eng.workerRootDir, "snapshots")
	eng.contentStoreRootDir = filepath.Join(eng.workerRootDir, "content")
	eng.containerdMetaDBPath = filepath.Join(eng.workerRootDir, "containerdmeta.db")
	eng.workerCacheMetaDBPath = filepath.Join(eng.workerRootDir, "metadata_v2.db")
	eng.buildkitMountPoolDir = filepath.Join(eng.workerRootDir, "cachemounts")

	eng.executorRootDir = filepath.Join(eng.workerRootDir, "executor")
	if err := os.MkdirAll(eng.executorRootDir, 0o711); err != nil {
		return nil, err
	}
	// clean up old hosts/resolv.conf file. ignore errors
	os.RemoveAll(filepath.Join(eng.executorRootDir, "hosts"))
	os.RemoveAll(filepath.Join(eng.executorRootDir, "resolv.conf"))

	//
	// setup config derived from engine config
	//

	for _, entStr := range cfg.Entitlements {
		ent, err := entitlements.Parse(entStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse entitlement %s: %w", entStr, err)
		}
		eng.entitlements[ent] = struct{}{}
	}

	if platformsStr := ociCfg.Platforms; len(platformsStr) != 0 {
		var err error
		eng.enabledPlatforms, err = parsePlatforms(platformsStr)
		if err != nil {
			return nil, fmt.Errorf("invalid platforms: %w", err)
		}
	}
	if len(eng.enabledPlatforms) == 0 {
		eng.enabledPlatforms = []ocispecs.Platform{platforms.Normalize(platforms.DefaultSpec())}
	}

	eng.registryHosts = resolver.NewRegistryConfig(cfg.Registries)

	if slog.Default().Enabled(ctx, slog.LevelExtraDebug) {
		eng.buildkitLogSink = os.Stderr
	}

	//
	// setup various buildkit/containerd entities and DBs
	//

	eng.bkSessionManager, err = bksession.NewManager()
	if err != nil {
		return nil, err
	}

	eng.snapshotter, eng.snapshotterName, err = newSnapshotter(eng.snapshotterRootDir, ociCfg, eng.bkSessionManager, eng.registryHosts)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshotter: %w", err)
	}

	eng.localContentStore, err = local.NewStore(eng.contentStoreRootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create content store: %w", err)
	}

	eng.containerdMetaBoltDB, err = bolt.Open(eng.containerdMetaDBPath, 0644, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open metadata db: %w", err)
	}

	eng.containerdMetaDB = ctdmetadata.NewDB(eng.containerdMetaBoltDB, eng.localContentStore, map[string]ctdsnapshot.Snapshotter{
		eng.snapshotterName: eng.snapshotter,
	})
	if err := eng.containerdMetaDB.Init(context.TODO()); err != nil {
		return nil, fmt.Errorf("failed to init metadata db: %w", err)
	}

	eng.leaseManager = leaseutil.WithNamespace(ctdmetadata.NewLeaseManager(eng.containerdMetaDB), "buildkit")
	eng.workerCacheMetaDB, err = metadata.NewStore(eng.workerCacheMetaDBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create metadata store: %w", err)
	}

	eng.contentStore = containerdsnapshot.NewContentStore(eng.containerdMetaDB.ContentStore(), "buildkit")

	//
	// setup worker+executor
	//

	eng.runc = &runc.Runc{
		Command:      distconsts.RuncPath,
		Log:          filepath.Join(eng.executorRootDir, "runc-log.json"),
		LogFormat:    runc.JSON,
		Setpgid:      true,
		PdeathSignal: syscall.SIGKILL,
	}

	var npResolvedMode string
	eng.networkProviders, npResolvedMode, err = netproviders.Providers(netproviders.Opt{
		Mode: cfg.Workers.OCI.NetworkConfig.Mode,
		CNI: cniprovider.Opt{
			Root:       eng.rootDir,
			ConfigPath: cfg.Workers.OCI.CNIConfigPath,
			BinaryDir:  cfg.Workers.OCI.CNIBinaryPath,
			PoolSize:   cfg.Workers.OCI.CNIPoolSize,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create network providers: %w", err)
	}

	if ociCfg.MaxParallelism > 0 {
		eng.parallelismSem = semaphore.NewWeighted(int64(ociCfg.MaxParallelism))
		ociCfg.Labels["maxParallelism"] = strconv.Itoa(ociCfg.MaxParallelism)
	}

	baseLabels := map[string]string{
		wlabel.Executor:       "oci",
		wlabel.Snapshotter:    eng.snapshotterName,
		wlabel.Network:        npResolvedMode,
		wlabel.OCIProcessMode: eng.processMode.String(),
		wlabel.SELinuxEnabled: strconv.FormatBool(ociCfg.SELinux),
	}
	if ociCfg.ApparmorProfile != "" {
		baseLabels[wlabel.ApparmorProfile] = ociCfg.ApparmorProfile
	}
	if hostname, err := os.Hostname(); err != nil {
		baseLabels[wlabel.Hostname] = "unknown"
	} else {
		baseLabels[wlabel.Hostname] = hostname
	}
	for k, v := range ociCfg.Labels {
		baseLabels[k] = v
	}
	workerID, err := base.ID(eng.workerRootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get worker ID: %w", err)
	}

	eng.baseWorker, err = base.NewWorker(ctx, base.WorkerOpt{
		ID:        workerID,
		Labels:    baseLabels,
		Platforms: eng.enabledPlatforms,
		GCPolicy:  getGCPolicy(ociCfg.GCConfig, eng.rootDir),
		BuildkitVersion: bkclient.BuildkitVersion{
			Package:  version.Package,
			Version:  version.Version,
			Revision: version.Revision,
		},
		NetworkProviders: eng.networkProviders,
		Executor:         nil, // not needed yet, set in clientWorker
		Snapshotter: containerdsnapshot.NewSnapshotter(
			eng.snapshotterName,
			eng.containerdMetaDB.Snapshotter(eng.snapshotterName),
			"buildkit",
			nil, // no idmapping
		),
		ContentStore:    eng.contentStore,
		Applier:         winlayers.NewFileSystemApplierWithWindows(eng.contentStore, apply.NewFileSystemApplier(eng.contentStore)),
		Differ:          winlayers.NewWalkingDiffWithWindows(eng.contentStore, walking.NewWalkingDiff(eng.contentStore)),
		ImageStore:      nil, // explicitly, because that's what upstream does too
		RegistryHosts:   eng.registryHosts,
		IdentityMapping: nil, // no idmapping
		LeaseManager:    eng.leaseManager,
		GarbageCollect:  eng.containerdMetaDB.GarbageCollect,
		ParallelismSem:  eng.parallelismSem,
		MetadataStore:   eng.workerCacheMetaDB,
		MountPoolRoot:   eng.buildkitMountPoolDir,
		ResourceMonitor: nil, // we don't use it
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create base worker: %w", err)
	}
	eng.workerCache = eng.baseWorker.CacheMgr
	eng.workerSourceManager = eng.baseWorker.SourceManager

	logrus.Infof("found worker %q, labels=%v, platforms=%v", workerID, baseLabels, FormatPlatforms(eng.enabledPlatforms))
	archutil.WarnIfUnsupported(eng.enabledPlatforms)

	// registerDaggerCustomSources adds Dagger's custom sources to the worker.
	hs, err := httpdns.NewSource(httpdns.Opt{
		Opt: srchttp.Opt{
			CacheAccessor: eng.workerCache,
		},
		BaseDNSConfig: eng.dns,
	})
	if err != nil {
		return nil, err
	}
	eng.workerSourceManager.Register(hs)

	gs, err := gitdns.NewSource(gitdns.Opt{
		Opt: srcgit.Opt{
			CacheAccessor: eng.workerCache,
		},
		BaseDNSConfig: eng.dns,
	})
	if err != nil {
		return nil, err
	}
	eng.workerSourceManager.Register(gs)

	bs, err := blob.NewSource(blob.Opt{
		CacheAccessor: eng.workerCache,
	})
	if err != nil {
		return nil, err
	}
	eng.workerSourceManager.Register(bs)

	eng.worker = buildkit.NewWorker(&buildkit.NewWorkerOpts{
		WorkerRoot:      eng.workerRootDir,
		ExecutorRoot:    eng.executorRootDir,
		BaseWorker:      eng.baseWorker,
		Controller:      eng,
		TelemetryPubSub: eng.telemetryPubSub,

		Runc:                eng.runc,
		DefaultCgroupParent: eng.cgroupParent,
		ProcessMode:         eng.processMode,
		IDMapping:           nil, // no idmapping
		DNSConfig:           eng.dns,
		ApparmorProfile:     eng.apparmorProfile,
		SELinux:             eng.selinux,
		Entitlements:        eng.entitlements,
		NetworkProviders:    eng.networkProviders,
		ParallelismSem:      eng.parallelismSem,
		WorkerCache:         eng.workerCache,
	})

	//
	// setup solver
	//

	baseWorkerController, err := buildkit.AsWorkerController(eng.worker)
	if err != nil {
		return nil, fmt.Errorf("failed to create worker controller: %w", err)
	}
	eng.frontends["dockerfile.v0"] = forwarder.NewGatewayForwarder(baseWorkerController.Infos(), dockerfile.Build)
	frontendGateway, err := gateway.NewGatewayFrontend(baseWorkerController.Infos(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create gateway frontend: %w", err)
	}
	eng.frontends["gateway.v0"] = frontendGateway

	eng.solverCacheDB, err = bboltcachestorage.NewStore(eng.solverCacheDBPath)
	if err != nil {
		return nil, err
	}

	cacheServiceURL := os.Getenv("_EXPERIMENTAL_DAGGER_CACHESERVICE_URL")
	cacheServiceToken := os.Getenv("_EXPERIMENTAL_DAGGER_CACHESERVICE_TOKEN")
	// add DAGGER_CLOUD_TOKEN in a backwards compat way.
	// TODO: deprecate in a future release
	if v, ok := os.LookupEnv("DAGGER_CLOUD_TOKEN"); ok {
		cacheServiceToken = v
	}

	if cacheServiceURL == "" {
		cacheServiceURL = daggerCacheServiceURL
	}
	eng.SolverCache, err = daggercache.NewManager(ctx, daggercache.ManagerConfig{
		KeyStore:     eng.solverCacheDB,
		ResultStore:  bkworker.NewCacheResultStorage(baseWorkerController),
		Worker:       eng.baseWorker,
		MountManager: mounts.NewMountManager("dagger-cache", eng.workerCache, eng.bkSessionManager),
		ServiceURL:   cacheServiceURL,
		Token:        cacheServiceToken,
		EngineID:     opts.EngineName,
	})
	if err != nil {
		return nil, err
	}

	eng.cacheExporters = map[string]remotecache.ResolveCacheExporterFunc{
		"registry": registryremotecache.ResolveCacheExporterFunc(eng.bkSessionManager, eng.registryHosts),
		"local":    localremotecache.ResolveCacheExporterFunc(eng.bkSessionManager),
		"inline":   inlineremotecache.ResolveCacheExporterFunc(),
		"gha":      gha.ResolveCacheExporterFunc(),
		"s3":       s3remotecache.ResolveCacheExporterFunc(),
		"azblob":   azblob.ResolveCacheExporterFunc(),
	}
	eng.cacheImporters = map[string]remotecache.ResolveCacheImporterFunc{
		"registry": registryremotecache.ResolveCacheImporterFunc(eng.bkSessionManager, eng.contentStore, eng.registryHosts),
		"local":    localremotecache.ResolveCacheImporterFunc(eng.bkSessionManager),
		"gha":      gha.ResolveCacheImporterFunc(),
		"s3":       s3remotecache.ResolveCacheImporterFunc(),
		"azblob":   azblob.ResolveCacheImporterFunc(),
	}

	eng.solver = solver.NewSolver(solver.SolverOpt{
		ResolveOpFunc: func(vtx solver.Vertex, builder solver.Builder) (solver.Op, error) {
			var w *buildkit.Worker
			if err := builder.EachValue(context.Background(), buildkit.DaggerWorkerJobKey,
				func(v interface{}) error {
					if w == nil {
						w, _ = v.(*buildkit.Worker)
					}
					return nil
				},
			); err != nil {
				return nil, fmt.Errorf("failed to get worker from job keys: %w", err)
			}
			if w == nil {
				return nil, fmt.Errorf("worker not found in job keys")
			}

			// passing nil bridge since it's only needed for BuildOp, which is never used and
			// never should be used (it's a legacy API)
			return w.ResolveOp(vtx, nil, eng.bkSessionManager)
		},
		DefaultCache: eng.SolverCache,
	})

	eng.throttledGC = throttle.After(time.Minute, eng.gc)
	defer func() {
		time.AfterFunc(time.Second, eng.throttledGC)
	}()

	return eng, nil
}

func (e *Engine) Close() error {
	err := e.baseWorker.Close()

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

func (e *Engine) Info(ctx context.Context, r *controlapi.InfoRequest) (*controlapi.InfoResponse, error) {
	return &controlapi.InfoResponse{
		BuildkitVersion: &apitypes.BuildkitVersion{
			Package:  engine.Package,
			Version:  engine.Version,
			Revision: e.engineName,
		},
	}, nil
}

func (e *Engine) ListWorkers(ctx context.Context, r *controlapi.ListWorkersRequest) (*controlapi.ListWorkersResponse, error) {
	resp := &controlapi.ListWorkersResponse{
		Record: []*apitypes.WorkerRecord{{
			ID:        e.worker.ID(),
			Labels:    e.worker.Labels(),
			Platforms: pb.PlatformsFromSpec(e.enabledPlatforms),
		}},
	}
	return resp, nil
}

func (e *Engine) LogMetrics(l *logrus.Entry) *logrus.Entry {
	e.serverMu.RLock()
	defer e.serverMu.RUnlock()
	l = l.WithField("dagger-server-count", len(e.servers))
	for _, s := range e.servers {
		l = s.LogMetrics(l)
	}
	return l
}

func (e *Engine) Register(server *grpc.Server) {
	controlapi.RegisterControlServer(server, e)

	traceSrv := &enginetel.TraceServer{PubSub: e.telemetryPubSub}
	tracev1.RegisterTraceServiceServer(server, traceSrv)
	enginetel.RegisterTracesSourceServer(server, traceSrv)

	logsSrv := &enginetel.LogsServer{PubSub: e.telemetryPubSub}
	logsv1.RegisterLogsServiceServer(server, logsSrv)
	enginetel.RegisterLogsSourceServer(server, logsSrv)

	metricsSrv := &enginetel.MetricsServer{PubSub: e.telemetryPubSub}
	metricsv1.RegisterMetricsServiceServer(server, metricsSrv)
	enginetel.RegisterMetricsSourceServer(server, metricsSrv)
}

func (e *Engine) Solve(ctx context.Context, req *controlapi.SolveRequest) (*controlapi.SolveResponse, error) {
	return nil, fmt.Errorf("solve not implemented")
}

func (e *Engine) Status(req *controlapi.StatusRequest, stream controlapi.Control_StatusServer) error {
	return fmt.Errorf("status not implemented")
}

func (e *Engine) ListenBuildHistory(req *controlapi.BuildHistoryRequest, srv controlapi.Control_ListenBuildHistoryServer) error {
	return fmt.Errorf("listen build history not implemented")
}

func (e *Engine) UpdateBuildHistory(ctx context.Context, req *controlapi.UpdateBuildHistoryRequest) (*controlapi.UpdateBuildHistoryResponse, error) {
	return nil, fmt.Errorf("update build history not implemented")
}

func retry[T any](ctx context.Context, interval time.Duration, maxRetries int, f func() (T, error)) (T, error) {
	var err error
	for i := 0; i < maxRetries; i++ {
		var t T
		t, err = f()
		if err == nil {
			return t, nil
		}
		select {
		case <-time.After(interval):
		case <-ctx.Done():
			return t, ctx.Err()
		}
	}
	var t T
	return t, err
}
