package server

import (
	"context"
	"errors"
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
	"github.com/containerd/containerd/remotes/docker"
	ctdsnapshot "github.com/containerd/containerd/snapshots"
	"github.com/containerd/go-runc"
	"github.com/containerd/platforms"
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
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	daggercache "github.com/dagger/dagger/engine/cache"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/sources/blob"
	"github.com/dagger/dagger/engine/sources/gitdns"
	"github.com/dagger/dagger/engine/sources/httpdns"
)

const (
	daggerCacheServiceURL = "https://api.dagger.cloud/magicache"
)

type Server struct {
	controlapi.UnimplementedControlServer
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
	clientDBDir           string

	//
	// buildkit+containerd entities/DBs
	//

	baseWorker            *base.Worker
	worker                *buildkit.Worker
	workerCacheMetaDB     *metadata.Store
	workerCache           bkcache.Manager
	workerSourceManager   *source.Manager
	workerDefaultGCPolicy bkclient.PruneInfo

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
	defaultPlatform  ocispecs.Platform
	registryHosts    docker.RegistryHosts

	//
	// telemetry config+state
	//

	telemetryPubSub *PubSub
	buildkitLogSink io.Writer

	//
	// gc related
	//
	throttledGC func()
	gcmu        sync.Mutex

	//
	// session+client state
	//
	daggerSessions   map[string]*daggerSession // session id -> session state
	daggerSessionsMu sync.RWMutex
	clientDBs        *clientdb.DBs
}

type NewServerOpts struct {
	Config *config.Config
	Name   string
}

//nolint:gocyclo
func NewServer(ctx context.Context, opts *NewServerOpts) (*Server, error) {
	cfg := opts.Config
	ociCfg := cfg.Workers.OCI

	srv := &Server{
		engineName: opts.Name,

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

		daggerSessions: make(map[string]*daggerSession),
	}

	//
	// setup directories and paths
	//

	var err error
	srv.rootDir, err = filepath.Abs(srv.rootDir)
	if err != nil {
		return nil, err
	}
	srv.rootDir, err = filepath.EvalSymlinks(srv.rootDir)
	if err != nil {
		return nil, err
	}
	srv.solverCacheDBPath = filepath.Join(srv.rootDir, "cache.db")

	srv.workerRootDir = filepath.Join(srv.rootDir, "worker")
	if err := os.MkdirAll(srv.workerRootDir, 0700); err != nil {
		return nil, err
	}
	srv.snapshotterRootDir = filepath.Join(srv.workerRootDir, "snapshots")
	srv.contentStoreRootDir = filepath.Join(srv.workerRootDir, "content")
	srv.containerdMetaDBPath = filepath.Join(srv.workerRootDir, "containerdmeta.db")
	srv.workerCacheMetaDBPath = filepath.Join(srv.workerRootDir, "metadata_v2.db")
	srv.buildkitMountPoolDir = filepath.Join(srv.workerRootDir, "cachemounts")

	srv.executorRootDir = filepath.Join(srv.workerRootDir, "executor")
	if err := os.MkdirAll(srv.executorRootDir, 0o711); err != nil {
		return nil, err
	}
	// clean up old hosts/resolv.conf file. ignore errors
	os.RemoveAll(filepath.Join(srv.executorRootDir, "hosts"))
	os.RemoveAll(filepath.Join(srv.executorRootDir, "resolv.conf"))

	// set up client DBs, and the telemetry pub/sub which writes to it
	srv.clientDBDir = filepath.Join(srv.workerRootDir, "clientdbs")
	srv.clientDBs = clientdb.NewDBs(srv.clientDBDir)
	srv.telemetryPubSub = NewPubSub(srv)

	//
	// setup config derived from engine config
	//

	for _, entStr := range cfg.Entitlements {
		ent, err := entitlements.Parse(entStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse entitlement %s: %w", entStr, err)
		}
		srv.entitlements[ent] = struct{}{}
	}

	srv.defaultPlatform = platforms.Normalize(platforms.DefaultSpec())
	if platformsStr := ociCfg.Platforms; len(platformsStr) != 0 {
		var err error
		srv.enabledPlatforms, err = parsePlatforms(platformsStr)
		if err != nil {
			return nil, fmt.Errorf("invalid platforms: %w", err)
		}
	}
	if len(srv.enabledPlatforms) == 0 {
		srv.enabledPlatforms = []ocispecs.Platform{srv.defaultPlatform}
	}

	srv.registryHosts = resolver.NewRegistryConfig(cfg.Registries)

	if slog.Default().Enabled(ctx, slog.LevelExtraDebug) {
		srv.buildkitLogSink = os.Stderr
	}

	//
	// setup various buildkit/containerd entities and DBs
	//

	srv.bkSessionManager, err = bksession.NewManager()
	if err != nil {
		return nil, err
	}

	srv.snapshotter, srv.snapshotterName, err = newSnapshotter(srv.snapshotterRootDir, ociCfg, srv.bkSessionManager, srv.registryHosts)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshotter: %w", err)
	}

	srv.localContentStore, err = local.NewStore(srv.contentStoreRootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create content store: %w", err)
	}

	srv.containerdMetaBoltDB, err = bolt.Open(srv.containerdMetaDBPath, 0644, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open metadata db: %w", err)
	}

	srv.containerdMetaDB = ctdmetadata.NewDB(srv.containerdMetaBoltDB, srv.localContentStore, map[string]ctdsnapshot.Snapshotter{
		srv.snapshotterName: srv.snapshotter,
	})
	if err := srv.containerdMetaDB.Init(context.TODO()); err != nil {
		return nil, fmt.Errorf("failed to init metadata db: %w", err)
	}

	srv.leaseManager = leaseutil.WithNamespace(ctdmetadata.NewLeaseManager(srv.containerdMetaDB), "buildkit")
	srv.workerCacheMetaDB, err = metadata.NewStore(srv.workerCacheMetaDBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create metadata store: %w", err)
	}

	srv.contentStore = containerdsnapshot.NewContentStore(srv.containerdMetaDB.ContentStore(), "buildkit")

	//
	// setup worker+executor
	//

	srv.runc = &runc.Runc{
		Command:      distconsts.RuncPath,
		Log:          filepath.Join(srv.executorRootDir, "runc-log.json"),
		LogFormat:    runc.JSON,
		Setpgid:      true,
		PdeathSignal: syscall.SIGKILL,
	}

	var npResolvedMode string
	srv.networkProviders, npResolvedMode, err = netproviders.Providers(netproviders.Opt{
		Mode: cfg.Workers.OCI.NetworkConfig.Mode,
		CNI: cniprovider.Opt{
			Root:       srv.rootDir,
			ConfigPath: cfg.Workers.OCI.CNIConfigPath,
			BinaryDir:  cfg.Workers.OCI.CNIBinaryPath,
			PoolSize:   cfg.Workers.OCI.CNIPoolSize,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create network providers: %w", err)
	}

	if ociCfg.MaxParallelism > 0 {
		srv.parallelismSem = semaphore.NewWeighted(int64(ociCfg.MaxParallelism))
		ociCfg.Labels["maxParallelism"] = strconv.Itoa(ociCfg.MaxParallelism)
	}

	baseLabels := map[string]string{
		wlabel.Executor:       "oci",
		wlabel.Snapshotter:    srv.snapshotterName,
		wlabel.Network:        npResolvedMode,
		wlabel.OCIProcessMode: srv.processMode.String(),
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
	workerID, err := base.ID(srv.workerRootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get worker ID: %w", err)
	}

	srv.baseWorker, err = base.NewWorker(ctx, base.WorkerOpt{
		ID:        workerID,
		Labels:    baseLabels,
		Platforms: srv.enabledPlatforms,
		GCPolicy:  getGCPolicy(ociCfg.GCConfig, srv.rootDir),
		BuildkitVersion: bkclient.BuildkitVersion{
			Package:  version.Package,
			Version:  version.Version,
			Revision: version.Revision,
		},
		NetworkProviders: srv.networkProviders,
		Executor:         nil, // not needed yet, set in clientWorker
		Snapshotter: containerdsnapshot.NewSnapshotter(
			srv.snapshotterName,
			srv.containerdMetaDB.Snapshotter(srv.snapshotterName),
			"buildkit",
			nil, // no idmapping
		),
		ContentStore:    srv.contentStore,
		Applier:         winlayers.NewFileSystemApplierWithWindows(srv.contentStore, apply.NewFileSystemApplier(srv.contentStore)),
		Differ:          winlayers.NewWalkingDiffWithWindows(srv.contentStore, walking.NewWalkingDiff(srv.contentStore)),
		ImageStore:      nil, // explicitly, because that's what upstream does too
		RegistryHosts:   srv.registryHosts,
		IdentityMapping: nil, // no idmapping
		LeaseManager:    srv.leaseManager,
		GarbageCollect:  srv.containerdMetaDB.GarbageCollect,
		ParallelismSem:  srv.parallelismSem,
		MetadataStore:   srv.workerCacheMetaDB,
		Root:            srv.rootDir,
		MountPoolRoot:   srv.buildkitMountPoolDir,
		ResourceMonitor: nil, // we don't use it
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create base worker: %w", err)
	}
	srv.workerCache = srv.baseWorker.CacheMgr
	srv.workerSourceManager = srv.baseWorker.SourceManager
	srv.workerDefaultGCPolicy = getDefaultGCPolicy(ociCfg.GCConfig, srv.rootDir)

	logrus.Infof("found worker %q, labels=%v, platforms=%v", workerID, baseLabels, FormatPlatforms(srv.enabledPlatforms))
	archutil.WarnIfUnsupported(srv.enabledPlatforms)

	// registerDaggerCustomSources adds Dagger's custom sources to the worker.
	hs, err := httpdns.NewSource(httpdns.Opt{
		Opt: srchttp.Opt{
			CacheAccessor: srv.workerCache,
		},
		BaseDNSConfig: srv.dns,
	})
	if err != nil {
		return nil, err
	}
	srv.workerSourceManager.Register(hs)

	gs, err := gitdns.NewSource(gitdns.Opt{
		Opt: srcgit.Opt{
			CacheAccessor: srv.workerCache,
		},
		BaseDNSConfig: srv.dns,
	})
	if err != nil {
		return nil, err
	}
	srv.workerSourceManager.Register(gs)

	bs, err := blob.NewSource(blob.Opt{
		CacheAccessor: srv.workerCache,
	})
	if err != nil {
		return nil, err
	}
	srv.workerSourceManager.Register(bs)

	srv.worker = buildkit.NewWorker(&buildkit.NewWorkerOpts{
		WorkerRoot:       srv.workerRootDir,
		ExecutorRoot:     srv.executorRootDir,
		BaseWorker:       srv.baseWorker,
		TelemetryPubSub:  srv.telemetryPubSub,
		BKSessionManager: srv.bkSessionManager,
		SessionHandler:   srv,

		Runc:                srv.runc,
		DefaultCgroupParent: srv.cgroupParent,
		ProcessMode:         srv.processMode,
		IDMapping:           nil, // no idmapping
		DNSConfig:           srv.dns,
		ApparmorProfile:     srv.apparmorProfile,
		SELinux:             srv.selinux,
		Entitlements:        srv.entitlements,
		NetworkProviders:    srv.networkProviders,
		ParallelismSem:      srv.parallelismSem,
		WorkerCache:         srv.workerCache,
	})

	//
	// setup solver
	//

	baseWorkerController, err := buildkit.AsWorkerController(srv.worker)
	if err != nil {
		return nil, fmt.Errorf("failed to create worker controller: %w", err)
	}
	srv.frontends["dockerfile.v0"] = forwarder.NewGatewayForwarder(baseWorkerController.Infos(), dockerfile.Build)
	frontendGateway, err := gateway.NewGatewayFrontend(baseWorkerController.Infos(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create gateway frontend: %w", err)
	}
	srv.frontends["gateway.v0"] = frontendGateway

	srv.solverCacheDB, err = bboltcachestorage.NewStore(srv.solverCacheDBPath)
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
	srv.SolverCache, err = daggercache.NewManager(ctx, daggercache.ManagerConfig{
		KeyStore:     srv.solverCacheDB,
		ResultStore:  bkworker.NewCacheResultStorage(baseWorkerController),
		Worker:       srv.baseWorker,
		MountManager: mounts.NewMountManager("dagger-cache", srv.workerCache, srv.bkSessionManager),
		ServiceURL:   cacheServiceURL,
		Token:        cacheServiceToken,
		EngineID:     opts.Name,
	})
	if err != nil {
		return nil, err
	}

	srv.cacheExporters = map[string]remotecache.ResolveCacheExporterFunc{
		"registry": registryremotecache.ResolveCacheExporterFunc(srv.bkSessionManager, srv.registryHosts),
		"local":    localremotecache.ResolveCacheExporterFunc(srv.bkSessionManager),
		"inline":   inlineremotecache.ResolveCacheExporterFunc(),
		"gha":      gha.ResolveCacheExporterFunc(),
		"s3":       s3remotecache.ResolveCacheExporterFunc(),
		"azblob":   azblob.ResolveCacheExporterFunc(),
	}
	srv.cacheImporters = map[string]remotecache.ResolveCacheImporterFunc{
		"registry": registryremotecache.ResolveCacheImporterFunc(srv.bkSessionManager, srv.contentStore, srv.registryHosts),
		"local":    localremotecache.ResolveCacheImporterFunc(srv.bkSessionManager),
		"gha":      gha.ResolveCacheImporterFunc(),
		"s3":       s3remotecache.ResolveCacheImporterFunc(),
		"azblob":   azblob.ResolveCacheImporterFunc(),
	}

	srv.solver = solver.NewSolver(solver.SolverOpt{
		ResolveOpFunc: func(vtx solver.Vertex, builder solver.Builder) (solver.Op, error) {
			// passing nil bridge since it's only needed for BuildOp, which is never used and
			// never should be used (it's a legacy API)
			return srv.worker.ResolveOp(vtx, nil, srv.bkSessionManager)
		},
		DefaultCache: srv.SolverCache,
	})

	srv.throttledGC = throttle.After(time.Minute, srv.gc)
	defer func() {
		time.AfterFunc(time.Second, srv.throttledGC)
	}()

	// garbage collect client DBs
	go srv.gcClientDBs()

	return srv, nil
}

func (srv *Server) Close() error {
	err := srv.baseWorker.Close()

	// note this *could* cause a panic in Session if it was still running, so
	// the server should be shutdown first
	srv.daggerSessionsMu.Lock()
	daggerSessions := srv.daggerSessions
	srv.daggerSessions = nil
	srv.daggerSessionsMu.Unlock()

	for _, s := range daggerSessions {
		s.stateMu.Lock()
		err = errors.Join(err, srv.removeDaggerSession(context.Background(), s))
		s.stateMu.Unlock()
	}
	return err
}

func (srv *Server) Info(context.Context, *controlapi.InfoRequest) (*controlapi.InfoResponse, error) {
	return &controlapi.InfoResponse{
		BuildkitVersion: &apitypes.BuildkitVersion{
			Package:  engine.Package,
			Version:  engine.Version,
			Revision: srv.engineName,
		},
	}, nil
}

func (srv *Server) ListWorkers(context.Context, *controlapi.ListWorkersRequest) (*controlapi.ListWorkersResponse, error) {
	resp := &controlapi.ListWorkersResponse{
		Record: []*apitypes.WorkerRecord{{
			ID:        srv.worker.ID(),
			Labels:    srv.worker.Labels(),
			Platforms: pb.PlatformsFromSpec(srv.enabledPlatforms),
		}},
	}
	return resp, nil
}

func (srv *Server) LogMetrics(l *logrus.Entry) *logrus.Entry {
	srv.daggerSessionsMu.RLock()
	defer srv.daggerSessionsMu.RUnlock()
	l = l.WithField("dagger-session-count", len(srv.daggerSessions))
	/* TODO: FIX
	for _, s := range srv.daggerSessions {
		l = s.LogMetrics(l)
	}
	*/
	return l
}

func (srv *Server) Register(server *grpc.Server) {
	controlapi.RegisterControlServer(server, srv)
}

func (srv *Server) gcClientDBs() {
	for range time.NewTicker(time.Minute).C {
		if err := srv.clientDBs.GC(srv.activeClientIDs()); err != nil {
			slog.Error("failed to GC client DBs", "error", err)
		}
	}
}

func (srv *Server) activeClientIDs() map[string]bool {
	keep := map[string]bool{}

	srv.daggerSessionsMu.RLock()
	for _, sess := range srv.daggerSessions {
		for id := range sess.clients {
			keep[id] = true
		}
	}
	srv.daggerSessionsMu.RUnlock()

	return keep
}
