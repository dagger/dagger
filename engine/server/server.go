package server

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/containerd/content"
	localcontentstore "github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/diff/apply"
	"github.com/containerd/containerd/diff/walking"
	ctdmetadata "github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/remotes/docker"
	ctdsnapshot "github.com/containerd/containerd/snapshots"
	"github.com/containerd/go-runc"
	"github.com/containerd/platforms"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/cache"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/engine/filesync"
	controlapi "github.com/dagger/dagger/internal/buildkit/api/services/control"
	apitypes "github.com/dagger/dagger/internal/buildkit/api/types"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	"github.com/dagger/dagger/internal/buildkit/cache/metadata"
	"github.com/dagger/dagger/internal/buildkit/cache/remotecache"
	"github.com/dagger/dagger/internal/buildkit/cache/remotecache/azblob"
	"github.com/dagger/dagger/internal/buildkit/cache/remotecache/gha"
	inlineremotecache "github.com/dagger/dagger/internal/buildkit/cache/remotecache/inline"
	localremotecache "github.com/dagger/dagger/internal/buildkit/cache/remotecache/local"
	registryremotecache "github.com/dagger/dagger/internal/buildkit/cache/remotecache/registry"
	s3remotecache "github.com/dagger/dagger/internal/buildkit/cache/remotecache/s3"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	"github.com/dagger/dagger/internal/buildkit/frontend"
	dockerfile "github.com/dagger/dagger/internal/buildkit/frontend/dockerfile/builder"
	"github.com/dagger/dagger/internal/buildkit/frontend/gateway"
	"github.com/dagger/dagger/internal/buildkit/frontend/gateway/forwarder"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	containerdsnapshot "github.com/dagger/dagger/internal/buildkit/snapshot/containerd"
	"github.com/dagger/dagger/internal/buildkit/solver"
	"github.com/dagger/dagger/internal/buildkit/solver/bboltcachestorage"
	"github.com/dagger/dagger/internal/buildkit/solver/llbsolver/mounts"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/source"
	"github.com/dagger/dagger/internal/buildkit/util/archutil"
	"github.com/dagger/dagger/internal/buildkit/util/entitlements"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	"github.com/dagger/dagger/internal/buildkit/util/network"
	"github.com/dagger/dagger/internal/buildkit/util/network/cniprovider"
	"github.com/dagger/dagger/internal/buildkit/util/network/netproviders"
	"github.com/dagger/dagger/internal/buildkit/util/resolver"
	resolverconfig "github.com/dagger/dagger/internal/buildkit/util/resolver/config"
	"github.com/dagger/dagger/internal/buildkit/util/throttle"
	"github.com/dagger/dagger/internal/buildkit/util/winlayers"
	"github.com/dagger/dagger/internal/buildkit/version"
	bkworker "github.com/dagger/dagger/internal/buildkit/worker"
	"github.com/dagger/dagger/internal/buildkit/worker/base"
	wlabel "github.com/dagger/dagger/internal/buildkit/worker/label"
	"github.com/moby/locker"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	daggercache "github.com/dagger/dagger/engine/cache/cachemanager"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/sources/blob"
	"github.com/dagger/dagger/engine/sources/local"
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
	workerDefaultGCPolicy *bkclient.PruneInfo

	bkSessionManager *bksession.Manager

	solver               *solver.Solver
	solverCacheDB        *bboltcachestorage.Store
	SolverCache          daggercache.Manager
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
	// worker file syncer
	//

	workerFileSyncer *filesync.FileSyncer

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
	throttledGC                  func()
	throttledReleaseUnreferenced func()
	gcmu                         sync.Mutex

	//
	// dagql cache
	//
	baseDagqlCache cache.Cache[string, dagql.AnyResult]

	//
	// session+client state
	//
	daggerSessions   map[string]*daggerSession // session id -> session state
	daggerSessionsMu sync.RWMutex
	clientDBs        *clientdb.DBs

	locker *locker.Locker

	secretSalt []byte
}

type NewServerOpts struct {
	Name           string
	Config         *config.Config
	BuildkitConfig *bkconfig.Config
}

//nolint:gocyclo
func NewServer(ctx context.Context, opts *NewServerOpts) (*Server, error) {
	cfg := opts.Config
	bkcfg := opts.BuildkitConfig
	ociCfg := bkcfg.Workers.OCI

	srv := &Server{
		engineName: opts.Name,

		rootDir: bkcfg.Root,

		frontends: map[string]frontend.Frontend{},

		cgroupParent:    ociCfg.DefaultCgroupParent,
		processMode:     oci.ProcessSandbox,
		apparmorProfile: ociCfg.ApparmorProfile,
		selinux:         ociCfg.SELinux,
		entitlements:    entitlements.Set{},
		dns: &oci.DNSConfig{
			Nameservers:   bkcfg.DNS.Nameservers,
			Options:       bkcfg.DNS.Options,
			SearchDomains: bkcfg.DNS.SearchDomains,
		},

		baseDagqlCache: cache.NewCache[string, dagql.AnyResult](),
		daggerSessions: make(map[string]*daggerSession),
		locker:         locker.New(),
	}

	// start the global namespace worker pool, which is used for running Go funcs
	// in container namespaces dynamically
	buildkit.GetGlobalNamespaceWorkerPool().Start()

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

	if cfg.Security != nil {
		// prioritize out config first if it's set
		if cfg.Security.InsecureRootCapabilities == nil || *cfg.Security.InsecureRootCapabilities {
			srv.entitlements[entitlements.EntitlementSecurityInsecure] = struct{}{}
		}
	} else if bkcfg.Entitlements != nil {
		// fallback to the dagger config
		for _, entStr := range bkcfg.Entitlements {
			ent, err := entitlements.Parse(entStr)
			if err != nil {
				return nil, fmt.Errorf("failed to parse entitlement %s: %w", entStr, err)
			}
			srv.entitlements[ent] = struct{}{}
		}
	} else {
		// no config? apply dagger-specific defaults
		srv.entitlements[entitlements.EntitlementSecurityInsecure] = struct{}{}
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

	registries := bkcfg.Registries
	if len(registries) == 0 {
		registries = map[string]resolverconfig.RegistryConfig{}
	}
	for k, v := range cfg.Registries {
		registries[k] = resolverconfig.RegistryConfig{
			Mirrors:   v.Mirrors,
			PlainHTTP: v.PlainHTTP,
			Insecure:  v.Insecure,
			RootCAs:   v.RootCAs,
		}
	}
	srv.registryHosts = resolver.NewRegistryConfig(registries)

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

	srv.localContentStore, err = localcontentstore.NewStore(srv.contentStoreRootDir)
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
		Mode: bkcfg.Workers.OCI.NetworkConfig.Mode,
		CNI: cniprovider.Opt{
			Root:       srv.rootDir,
			ConfigPath: bkcfg.Workers.OCI.CNIConfigPath,
			BinaryDir:  bkcfg.Workers.OCI.CNIBinaryPath,
			PoolSize:   bkcfg.Workers.OCI.CNIPoolSize,
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
	maps.Copy(baseLabels, ociCfg.Labels)
	workerID, err := base.ID(srv.workerRootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get worker ID: %w", err)
	}

	srv.baseWorker, err = base.NewWorker(ctx, base.WorkerOpt{
		ID:        workerID,
		Labels:    baseLabels,
		Platforms: srv.enabledPlatforms,
		GCPolicy:  getGCPolicy(*cfg, ociCfg.GCConfig, srv.rootDir),
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
	srv.workerDefaultGCPolicy = getDefaultGCPolicy(*cfg, ociCfg.GCConfig, srv.rootDir)

	logrus.Infof("found worker %q, labels=%v, platforms=%v", workerID, baseLabels, FormatPlatforms(srv.enabledPlatforms))
	archutil.WarnIfUnsupported(srv.enabledPlatforms)

	srv.workerFileSyncer = filesync.NewFileSyncer(filesync.FileSyncerOpt{
		CacheAccessor: srv.workerCache,
	})

	bs, err := blob.NewSource(blob.Opt{
		CacheAccessor: srv.workerCache,
	})
	if err != nil {
		return nil, err
	}
	srv.workerSourceManager.Register(bs)

	// Protection mechanism for llb.Local operations to not panic
	// if the operation is called.
	srv.workerSourceManager.Register(local.NewSource())

	srv.worker = buildkit.NewWorker(&buildkit.NewWorkerOpts{
		WorkerRoot:       srv.workerRootDir,
		ExecutorRoot:     srv.executorRootDir,
		BaseWorker:       srv.baseWorker,
		TelemetryPubSub:  srv.telemetryPubSub,
		BKSessionManager: srv.bkSessionManager,
		SessionHandler:   srv,
		DagqlServer:      srv,

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

	srv.SolverCache, err = daggercache.NewManager(ctx, daggercache.ManagerConfig{
		KeyStore:     srv.solverCacheDB,
		ResultStore:  bkworker.NewCacheResultStorage(baseWorkerController),
		Worker:       srv.baseWorker,
		MountManager: mounts.NewMountManager("dagger-cache", srv.workerCache, srv.bkSessionManager),
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
	// use longer interval for releaseUnreferencedCache deleting links quickly is less important
	srv.throttledReleaseUnreferenced = throttle.After(5*time.Minute, func() { srv.SolverCache.ReleaseUnreferenced(context.Background()) })
	defer func() {
		time.AfterFunc(time.Second, srv.throttledGC)
	}()

	// garbage collect client DBs
	go srv.gcClientDBs()

	// initialize the secret salt
	secretSaltPath := filepath.Join(srv.rootDir, "secret-salt")
	srv.secretSalt, err = os.ReadFile(secretSaltPath)
	if err != nil || len(srv.secretSalt) != 32 {
		if err != nil && !os.IsNotExist(err) {
			slog.Warn("failed to read secret salt", "error", err)
		}
		srv.secretSalt = make([]byte, 32)
		_, err = cryptorand.Read(srv.secretSalt)
		if err != nil {
			return nil, fmt.Errorf("failed to read secret salt rand bytes: %w", err)
		}
		err = os.WriteFile(secretSaltPath, srv.secretSalt, 0600)
		if err != nil {
			slog.Warn("failed to write secret salt", "error", err, "path", secretSaltPath)
		}
	}

	return srv, nil
}

func (srv *Server) Close() error {
	err := srv.baseWorker.Close()

	// Shutdown the global namespace worker pool
	buildkit.ShutdownGlobalNamespaceWorkerPool()

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

func (srv *Server) BuildkitCache() bkcache.Manager {
	return srv.workerCache
}

func (srv *Server) BuildkitSession() *bksession.Manager {
	return srv.bkSessionManager
}

func (srv *Server) FileSyncer() *filesync.FileSyncer {
	return srv.workerFileSyncer
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
	l = l.WithField("dagql-cache-size", srv.baseDagqlCache.Size())
	return l
}

func (srv *Server) Register(server *grpc.Server) {
	controlapi.RegisterControlServer(server, srv)
}

// ConnectedClients returns the number of currently connected clients
func (srv *Server) ConnectedClients() int {
	srv.daggerSessionsMu.RLock()
	defer srv.daggerSessionsMu.RUnlock()
	return len(srv.daggerSessions)
}

func (srv *Server) Locker() *locker.Locker {
	return srv.locker
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
