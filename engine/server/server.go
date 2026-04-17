package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff/apply"
	ctdmetadata "github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	ctdsnapshot "github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	localcontentstore "github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/containerd/containerd/v2/plugins/diff/walking"
	"github.com/containerd/go-runc"
	"github.com/containerd/platforms"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/config"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	containerdsnapshot "github.com/dagger/dagger/engine/snapshots/containerd"
	controlapi "github.com/dagger/dagger/internal/buildkit/api/services/control"
	apitypes "github.com/dagger/dagger/internal/buildkit/api/types"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/util/archutil"
	"github.com/dagger/dagger/internal/buildkit/util/disk"
	"github.com/dagger/dagger/internal/buildkit/util/entitlements"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	"github.com/dagger/dagger/internal/buildkit/util/network"
	"github.com/dagger/dagger/internal/buildkit/util/network/cniprovider"
	"github.com/dagger/dagger/internal/buildkit/util/network/netproviders"
	resolverconfig "github.com/dagger/dagger/internal/buildkit/util/resolver/config"
	"github.com/dagger/dagger/internal/buildkit/util/throttle"
	"github.com/dagger/dagger/internal/buildkit/util/winlayers"
	wlabel "github.com/dagger/dagger/internal/buildkit/worker/label"
	"github.com/moby/locker"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/engine/slog"
)

type Server struct {
	controlapi.UnimplementedControlServer
	engineName string

	//
	// state directory/db paths
	//

	rootDir string

	workerRootDir         string
	snapshotterRootDir    string
	snapshotterDBPath     string
	contentStoreRootDir   string
	containerdMetaDBPath  string
	workerCacheMetaDBPath string
	buildkitMountPoolDir  string
	executorRootDir       string
	clientDBDir           string

	//
	// buildkit+containerd entities/DBs
	//

	worker                *engineutil.Worker
	workerCache           bkcache.SnapshotManager
	workerGCPolicies      []dagql.CachePrunePolicy
	workerDefaultGCPolicy *dagql.CachePrunePolicy

	bkSessionManager *bksession.Manager

	containerdMetaBoltDB *bolt.DB
	containerdMetaDB     *ctdmetadata.DB
	localContentStore    content.Store
	contentStore         *containerdsnapshot.Store
	builtinContentStore  content.Store

	snapshotter        ctdsnapshot.Snapshotter
	snapshotterMDStore *storage.MetaStore // only set for overlay snapshotter right now
	snapshotterName    string
	leaseManager       *leaseutil.Manager

	corruptDBReset bool

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
	enabledPlatforms []ocispecs.Platform
	defaultPlatform  ocispecs.Platform
	registryHosts    docker.RegistryHosts
	cleanMntNS       *os.File

	//
	// telemetry config+state
	//

	telemetryPubSub *PubSub

	coreSchemaBase   *schema.CoreSchemaBase
	coreSchemaBaseMu sync.Mutex

	//
	// gc related
	//
	throttledGC func()
	gcmu        sync.Mutex

	shutdownCtx    context.Context
	shutdownCancel context.CancelCauseFunc
	shuttingDown   atomic.Bool

	//
	// dagql cache
	//
	engineCache *dagql.Cache

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

		daggerSessions: make(map[string]*daggerSession),

		locker: locker.New(),
	}
	srv.shutdownCtx, srv.shutdownCancel = context.WithCancelCause(context.Background())

	// start the global namespace worker pool, which is used for running Go funcs
	// in container namespaces dynamically
	engineutil.GetGlobalNamespaceWorkerPool().Start()

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

	srv.workerRootDir = filepath.Join(srv.rootDir, "worker")
	srv.snapshotterRootDir = filepath.Join(srv.workerRootDir, "snapshots")
	srv.snapshotterDBPath = filepath.Join(srv.snapshotterRootDir, "metadata.db")
	srv.contentStoreRootDir = filepath.Join(srv.workerRootDir, "content")
	srv.containerdMetaDBPath = filepath.Join(srv.workerRootDir, "containerdmeta.db")
	srv.workerCacheMetaDBPath = filepath.Join(srv.workerRootDir, "metadata_v2.db")
	srv.buildkitMountPoolDir = filepath.Join(srv.workerRootDir, "cachemounts")

	srv.executorRootDir = filepath.Join(srv.workerRootDir, "executor")

	//
	// setup various buildkit/containerd entities and DBs
	//

	if err := srv.mkdirBaseDirs(); err != nil {
		return nil, err
	}

	if err := srv.initBoltDBs(); err != nil {
		// It's possible for DBs to get corrupted because we run them w/ Sync: false (for performance)
		// Reset all our state, but set corruptDBReset so it can be reported via metrics
		srv.corruptDBReset = true
		slog.Error("failed to initialize boltdbs, resetting all local cache state", "error", err)

		// need to rm paths individually since srv.rootDir is often a mount (and thus rm'ing it gives
		// a "device busy" error)
		rootEnts, err := os.ReadDir(srv.rootDir)
		if err != nil {
			return nil, fmt.Errorf("failed to read root dir entries for boltdb reset: %w", err)
		}
		for _, ent := range rootEnts {
			p := filepath.Join(srv.rootDir, ent.Name())
			if err := os.RemoveAll(p); err != nil {
				return nil, fmt.Errorf("failed to remove dir after boltdb init failure: %w", err)
			}
		}

		// try again
		if err := srv.mkdirBaseDirs(); err != nil {
			return nil, err
		}
		if err := srv.initBoltDBs(); err != nil {
			return nil, fmt.Errorf("failed to initialize boltdbs after reset: %w", err)
		}
	}

	srv.snapshotter, srv.snapshotterName, err = newSnapshotter(srv.snapshotterRootDir, ociCfg, srv.snapshotterMDStore)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshotter: %w", err)
	}

	srv.localContentStore, err = localcontentstore.NewStore(srv.contentStoreRootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create content store: %w", err)
	}

	srv.containerdMetaDB = ctdmetadata.NewDB(srv.containerdMetaBoltDB, srv.localContentStore, map[string]ctdsnapshot.Snapshotter{
		srv.snapshotterName: srv.snapshotter,
	})
	if err := srv.containerdMetaDB.Init(context.TODO()); err != nil {
		return nil, fmt.Errorf("failed to init metadata db: %w", err)
	}

	srv.leaseManager = leaseutil.WithNamespace(ctdmetadata.NewLeaseManager(srv.containerdMetaDB), "dagger")

	srv.bkSessionManager, err = bksession.NewManager()
	if err != nil {
		return nil, err
	}

	srv.contentStore = containerdsnapshot.NewContentStore(srv.containerdMetaDB.ContentStore(), "dagger")

	//
	// clean up old hosts/resolv.conf file. ignore errors
	//
	os.RemoveAll(filepath.Join(srv.executorRootDir, "hosts"))
	os.RemoveAll(filepath.Join(srv.executorRootDir, "resolv.conf"))

	//
	// set up client DBs, and the telemetry pub/sub which writes to it
	//

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
			srv.entitlements[entitlements.EntitlementNetworkHost] = struct{}{}
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
		srv.entitlements[entitlements.EntitlementNetworkHost] = struct{}{}
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
	srv.registryHosts = newRegistryHosts(registries)

	srv.builtinContentStore, err = openBuiltinOCIStore()
	if err != nil {
		return nil, fmt.Errorf("failed to open builtin content store: %w", err)
	}

	//
	// setup worker+executor
	//

	srv.runc = &runc.Runc{
		Command:   distconsts.RuncPath,
		Log:       filepath.Join(srv.executorRootDir, "runc-log.json"),
		LogFormat: runc.JSON,
		Setpgid:   true,
		// TODO: this isn't technically needed (and breaks obscure things around goroutines+namespaces) right now,
		// but could be if we support the engine running outside a container someday
		// PdeathSignal: syscall.SIGKILL,
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

	workerSnapshotter := containerdsnapshot.NewSnapshotter(
		srv.snapshotterName,
		srv.containerdMetaDB.Snapshotter(srv.snapshotterName),
		"dagger",
	)

	workerGCPolicies := getDagqlGCPolicy(*cfg, ociCfg.GCConfig, srv.rootDir)
	workerOpt := engineutil.WorkerOpt{
		ID:               rand.Text(),
		Labels:           baseLabels,
		Platforms:        srv.enabledPlatforms,
		GCPolicy:         buildkitPruneInfosFromDagqlPolicies(workerGCPolicies),
		NetworkProviders: srv.networkProviders,
		Snapshotter:      workerSnapshotter,
		ContentStore:     srv.contentStore,
		Applier:          winlayers.NewFileSystemApplierWithWindows(srv.contentStore, apply.NewFileSystemApplier(srv.contentStore)),
		Differ:           winlayers.NewWalkingDiffWithWindows(srv.contentStore, walking.NewWalkingDiff(srv.contentStore)),
		ImageStore:       nil, // explicitly, because that's what upstream does too
		RegistryHosts:    srv.registryHosts,
		IdentityMapping:  nil, // no idmapping
		LeaseManager:     srv.leaseManager,
		Root:             srv.rootDir,
	}

	srv.workerCache, err = bkcache.NewSnapshotManager(bkcache.SnapshotManagerOpt{
		Snapshotter:   workerSnapshotter,
		ContentStore:  srv.contentStore,
		LeaseManager:  srv.leaseManager,
		Applier:       winlayers.NewFileSystemApplierWithWindows(srv.contentStore, apply.NewFileSystemApplier(srv.contentStore)),
		Differ:        winlayers.NewWalkingDiffWithWindows(srv.contentStore, walking.NewWalkingDiff(srv.contentStore)),
		MountPoolRoot: srv.buildkitMountPoolDir,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot manager: %w", err)
	}

	srv.workerGCPolicies = cloneDagqlCachePrunePolicies(workerGCPolicies)
	srv.workerDefaultGCPolicy = getDefaultDagqlGCPolicy(*cfg, ociCfg.GCConfig, srv.rootDir)

	archutil.WarnIfUnsupported(srv.enabledPlatforms)

	hostMntNS, err := os.OpenFile("/proc/self/ns/mnt", os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to open host mount namespace: %w", err)
	}

	var eg errgroup.Group
	eg.Go(func() error {
		runtime.LockOSThread()
		if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
			return fmt.Errorf("failed to create clean mount namespace: %w", err)
		}
		var err error
		srv.cleanMntNS, err = os.OpenFile("/proc/thread-self/ns/mnt", os.O_RDONLY, 0)
		if err != nil {
			return err
		}
		return nil
	})
	if err := eg.Wait(); err != nil {
		return nil, fmt.Errorf("failed to create clean mount namespace: %w", err)
	}

	srv.worker, err = engineutil.NewWorker(&engineutil.NewWorkerOpts{
		WorkerOpt:        workerOpt,
		WorkerRoot:       srv.workerRootDir,
		ExecutorRoot:     srv.executorRootDir,
		TelemetryPubSub:  srv.telemetryPubSub,
		BKSessionManager: srv.bkSessionManager,
		SessionHandler:   srv,
		DagqlServer:      srv,

		Runc:                srv.runc,
		DefaultCgroupParent: srv.cgroupParent,
		ProcessMode:         srv.processMode,
		DNSConfig:           srv.dns,
		ApparmorProfile:     srv.apparmorProfile,
		SELinux:             srv.selinux,
		Entitlements:        srv.entitlements,
		WorkerCache:         srv.workerCache,

		HostMntNS:  hostMntNS,
		CleanMntNS: srv.cleanMntNS,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create worker: %w", err)
	}

	//
	// setup solver
	//

	srv.throttledGC = throttle.After(time.Minute, srv.gc)
	defer func() {
		time.AfterFunc(time.Second, srv.throttledGC)
	}()

	//
	// setup dagql caching
	//
	dagqlCacheDBPath := filepath.Join(srv.rootDir, "dagql-cache.db")
	snapshotGC := func(ctx context.Context) error {
		stats, err := srv.containerdMetaDB.GarbageCollect(ctx)
		if err != nil {
			return err
		}
		slog.Debug("containerd garbage collect after dagql prune", "stats", stats)
		return nil
	}
	srv.engineCache, err = dagql.NewCache(ctx, dagqlCacheDBPath, srv.workerCache, snapshotGC)
	if err != nil {
		// Attempt to handle a corrupt db (which is possible since we currently run w/ synchronous=OFF) by removing any existing
		// db and trying again.
		slog.Error("failed to create dagql cache, attempting to recover by removing existing cache db", "error", err)
		if err := os.Remove(dagqlCacheDBPath); err != nil && !os.IsNotExist(err) {
			slog.Error("failed to remove existing dagql cache db", "error", err)
		}
		srv.engineCache, err = dagql.NewCache(ctx, dagqlCacheDBPath, srv.workerCache, snapshotGC)
		if err != nil {
			return nil, fmt.Errorf("failed to create dagql cache after removing existing db: %w", err)
		}
	}

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
		_, err = rand.Read(srv.secretSalt)
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

func (srv *Server) mkdirBaseDirs() (err error) {
	if err := os.MkdirAll(srv.workerRootDir, 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(srv.executorRootDir, 0o711); err != nil {
		return err
	}
	return nil
}

func (srv *Server) initBoltDBs() (err error) {
	defer func() {
		if panicErr := recover(); panicErr != nil {
			err = fmt.Errorf("panic while initializing boltdbs: %v", panicErr)
		}
	}()

	srv.snapshotterMDStore, err = storage.NewMetaStore(srv.snapshotterDBPath,
		func(opts *bolt.Options) error {
			opts.NoSync = true
			opts.NoFreelistSync = true
			opts.NoGrowSync = true
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create metadata store for snapshotter: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, srv.snapshotterMDStore.Close())
		}
	}()

	srv.containerdMetaBoltDB, err = bolt.Open(srv.containerdMetaDBPath, 0644, &bolt.Options{
		NoSync:         true,
		NoFreelistSync: true,
		NoGrowSync:     true,
	})
	if err != nil {
		return fmt.Errorf("failed to open metadata db: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, srv.containerdMetaBoltDB.Close())
		}
	}()

	return nil
}

var errServerShuttingDown = errors.New("engine is shutting down")

func (srv *Server) BeginGracefulStop() {
	if srv.shuttingDown.CompareAndSwap(false, true) && srv.shutdownCancel != nil {
		srv.shutdownCancel(errServerShuttingDown)
	}
}

func (srv *Server) isShuttingDown() bool {
	return srv != nil && srv.shuttingDown.Load()
}

func (srv *Server) withShutdownCancel(ctx context.Context) context.Context {
	if srv == nil || srv.shutdownCtx == nil {
		return ctx
	}
	ctx, cancel := context.WithCancelCause(ctx)
	go func() {
		select {
		case <-srv.shutdownCtx.Done():
			cancel(context.Cause(srv.shutdownCtx))
		case <-ctx.Done():
		}
	}()
	return ctx
}

func (srv *Server) EngineName() string {
	return srv.engineName
}

func (srv *Server) Clients() []string {
	srv.daggerSessionsMu.RLock()
	defer srv.daggerSessionsMu.RUnlock()

	clients := map[string]struct{}{}
	for _, sess := range srv.daggerSessions {
		clients[sess.mainClientCallerID] = struct{}{}
	}

	return slices.Collect(maps.Keys(clients))
}

// GracefulStop attempts to close all boltdbs and do a final syncfs since all the DBs
// run with NoSync=true (plus NoFreelistSync/NoGrowSync) for performance reasons.
func (srv *Server) GracefulStop(ctx context.Context) error {
	srv.BeginGracefulStop()

	var err error

	// note this *could* cause a panic in Session if it was still running, so
	// the server should be shutdown first
	srv.daggerSessionsMu.Lock()
	daggerSessions := srv.daggerSessions
	srv.daggerSessionsMu.Unlock()

	if srv.engineCache != nil {
		srv.gcmu.Lock()
		defer srv.gcmu.Unlock()
	}

	for _, s := range daggerSessions {
		s.stateMu.Lock()
		err = errors.Join(err, srv.removeDaggerSession(ctx, s))
		s.stateMu.Unlock()
	}

	if srv.engineCache != nil && len(srv.workerGCPolicies) > 0 {
		dstat, statErr := disk.GetDiskStat(srv.rootDir)
		if statErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to get disk stats for graceful shutdown prune: %w", statErr))
		} else {
			prunePolicies := cloneDagqlCachePrunePolicies(srv.workerGCPolicies)
			for i := range prunePolicies {
				prunePolicies[i].CurrentFreeSpace = dstat.Free
			}
			_, pruneErr := srv.engineCache.Prune(ctx, prunePolicies)
			if pruneErr != nil {
				err = errors.Join(err, fmt.Errorf("failed to prune dagql cache during graceful shutdown: %w", pruneErr))
			}
		}
	}

	if srv.engineCache != nil {
		if closeErr := srv.engineCache.Close(ctx); closeErr != nil {
			slog.Error("failed to close base dagql cache", "error", closeErr)
			err = errors.Join(err, closeErr)
		}
	}

	err = errors.Join(err, srv.worker.Close())

	// Shutdown the global namespace worker pool
	engineutil.ShutdownGlobalNamespaceWorkerPool()

	var eg errgroup.Group
	eg.Go(func() error {
		err := srv.snapshotterMDStore.Close()
		if err != nil {
			return fmt.Errorf("failed to close snapshotter metadata store: %w", err)
		}
		return nil
	})
	eg.Go(func() error {
		err := srv.containerdMetaBoltDB.Close()
		if err != nil {
			return fmt.Errorf("failed to close containerd metadata db: %w", err)
		}
		return nil
	})

	doneClosingCh := make(chan error)
	go func() {
		defer close(doneClosingCh)

		err := eg.Wait()
		defer func() {
			doneClosingCh <- err
		}()

		// all the DBs closed, do a final sync of the engine state filesystem.
		// Use a guaranteed child of the state root rather than the mountpoint
		// itself so path resolution is unambiguously inside the mounted tree.
		f, err := os.Open(srv.workerRootDir)
		if err != nil {
			err = fmt.Errorf("failed to open root dir for final sync: %w", err)
			return
		}
		defer f.Close()

		err = unix.Syncfs(int(f.Fd()))
		if err != nil {
			err = fmt.Errorf("failed to syncfs for final sync: %w", err)
			return
		}
	}()

	select {
	case err := <-doneClosingCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (srv *Server) SnapshotManager() bkcache.SnapshotManager {
	return srv.workerCache
}

func (srv *Server) CacheAccessor() bkcache.Accessor {
	return srv.workerCache
}

func (srv *Server) Info(context.Context, *controlapi.InfoRequest) (*controlapi.InfoResponse, error) {
	return &controlapi.InfoResponse{
		BuildkitVersion: &apitypes.BuildkitVersion{
			Package:  engine.Package,
			Version:  engine.Version,
			Revision: srv.engineName,
		},
		SystemInfo: &controlapi.SystemInfo{
			NumCPU: int32(runtime.NumCPU()),
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
	l = l.WithField("dagql-cache-size", srv.engineCache.Size())
	return l
}

func (srv *Server) Register(server *grpc.Server) {
	controlapi.RegisterControlServer(server, srv)
}

func (srv *Server) DagqlCacheEntries() int {
	if srv.engineCache == nil {
		return 0
	}
	return srv.engineCache.Size()
}

func (srv *Server) DagqlCacheEntryStats() dagql.CacheEntryStats {
	if srv.engineCache == nil {
		return dagql.CacheEntryStats{}
	}
	return srv.engineCache.EntryStats()
}

func (srv *Server) DagqlDebugSnapshot() *dagql.EGraphDebugSnapshot {
	if srv.engineCache == nil {
		return nil
	}
	return srv.engineCache.DebugEGraphSnapshot()
}

func (srv *Server) WriteDagqlCacheDebugSnapshot(w io.Writer) error {
	if srv.engineCache == nil {
		return fmt.Errorf("dagql cache not available")
	}
	return srv.engineCache.WriteDebugCacheSnapshot(w)
}

// ConnectedClients returns the number of currently connected clients
func (srv *Server) ConnectedClients() int {
	srv.daggerSessionsMu.RLock()
	defer srv.daggerSessionsMu.RUnlock()
	return len(srv.daggerSessions)
}

func (srv *Server) CorruptDBReset() bool {
	return srv.corruptDBReset
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
