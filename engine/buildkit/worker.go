package buildkit

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"

	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/diff/apply"
	"github.com/containerd/containerd/diff/walking"
	ctdmetadata "github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes/docker"
	ctdsnapshot "github.com/containerd/containerd/snapshots"
	runc "github.com/containerd/go-runc"
	"github.com/docker/docker/pkg/idtools"
	"github.com/moby/buildkit/cache/metadata"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/executor"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/frontend"
	"github.com/moby/buildkit/session"
	containerdsnapshot "github.com/moby/buildkit/snapshot/containerd"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/llbsolver/ops"
	"github.com/moby/buildkit/solver/pb"
	srcgit "github.com/moby/buildkit/source/git"
	srchttp "github.com/moby/buildkit/source/http"
	"github.com/moby/buildkit/util/entitlements"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/moby/buildkit/util/network"
	"github.com/moby/buildkit/util/network/netproviders"
	"github.com/moby/buildkit/util/winlayers"
	"github.com/moby/buildkit/worker"
	"github.com/moby/buildkit/worker/base"
	wlabel "github.com/moby/buildkit/worker/label"
	workerrunc "github.com/moby/buildkit/worker/runc"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/sync/semaphore"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/sources/blob"
	"github.com/dagger/dagger/engine/sources/gitdns"
	"github.com/dagger/dagger/engine/sources/httpdns"
)

/*
Worker is Dagger's custom worker. Most of the buildkit Worker interface methods are
just inherited from buildkit's base.Worker, with the exception of methods involving
executor.Executor (most importantly ResolveOp).

We need a custom Executor implementation for setting up containers (currently, just
for providing ServerID, but in the future everything the shim does will be migrated
here). For simplicity, this Worker struct also implements that Executor interface
(in executor.go) since Worker+Executor are so tightly bound together anyways.
*/
type Worker struct {
	*sharedWorkerState
	execMD *ExecutionMetadata
}

type Controller interface {
	HandleConn(context.Context, net.Conn, *engine.ClientMetadata, map[string][]string) error
}

type sharedWorkerState struct {
	*base.Worker
	Controller Controller
	root       string

	// executor specific
	runc             *runc.Runc
	executorRoot     string
	cgroupParent     string
	networkProviders map[pb.NetMode]network.Provider
	processMode      oci.ProcessMode
	idmap            *idtools.IdentityMapping
	dns              *oci.DNSConfig
	running          map[string]*execState
	mu               sync.RWMutex
	apparmorProfile  string
	selinux          bool
	tracingSocket    string
	entitlements     entitlements.Set
}

type NewWorkerOpts struct {
	Root                 string
	SnapshotterFactory   workerrunc.SnapshotterFactory
	ProcessMode          oci.ProcessMode
	Labels               map[string]string
	IDMapping            *idtools.IdentityMapping
	NetworkProvidersOpts netproviders.Opt
	DNSConfig            *oci.DNSConfig
	ApparmorProfile      string
	SELinux              bool
	ParallelismSem       *semaphore.Weighted
	TraceSocket          string
	DefaultCgroupParent  string
	Entitlements         []string
	GCPolicy             []client.PruneInfo
	BuildkitVersion      client.BuildkitVersion
	RegistryHosts        docker.RegistryHosts
	Platforms            []ocispecs.Platform
}

func NewWorker(ctx context.Context, opts *NewWorkerOpts) (*Worker, error) {
	var err error
	opts.Root, err = filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	opts.Root, err = filepath.EvalSymlinks(opts.Root)
	if err != nil {
		return nil, err
	}

	w := &Worker{sharedWorkerState: &sharedWorkerState{
		cgroupParent:    opts.DefaultCgroupParent,
		processMode:     opts.ProcessMode,
		idmap:           opts.IDMapping,
		dns:             opts.DNSConfig,
		running:         make(map[string]*execState),
		apparmorProfile: opts.ApparmorProfile,
		selinux:         opts.SELinux,
		tracingSocket:   opts.TraceSocket,
		entitlements:    entitlements.Set{},
	}}

	for _, entStr := range opts.Entitlements {
		ent, err := entitlements.Parse(entStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse entitlement %s: %w", entStr, err)
		}
		w.entitlements[ent] = struct{}{}
	}

	w.root = filepath.Join(opts.Root, "runc-"+opts.SnapshotterFactory.Name)
	if err := os.MkdirAll(w.root, 0700); err != nil {
		return nil, err
	}

	w.executorRoot = filepath.Join(w.root, "executor")
	if err := os.MkdirAll(w.executorRoot, 0o711); err != nil {
		return nil, fmt.Errorf("failed to create %s: %w", w.executorRoot, err)
	}
	// clean up old hosts/resolv.conf file. ignore errors
	os.RemoveAll(filepath.Join(w.executorRoot, "hosts"))
	os.RemoveAll(filepath.Join(w.executorRoot, "resolv.conf"))

	w.runc = &runc.Runc{
		Command:      distconsts.RuncPath,
		Log:          filepath.Join(w.executorRoot, "runc-log.json"),
		LogFormat:    runc.JSON,
		Setpgid:      true,
		PdeathSignal: syscall.SIGKILL,
	}

	var npResolvedMode string
	w.networkProviders, npResolvedMode, err = netproviders.Providers(opts.NetworkProvidersOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create network providers: %w", err)
	}

	snapshotter, err := opts.SnapshotterFactory.New(filepath.Join(w.root, "snapshots"))
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshotter: %w", err)
	}

	localstore, err := local.NewStore(filepath.Join(w.root, "content"))
	if err != nil {
		return nil, fmt.Errorf("failed to create content store: %w", err)
	}

	db, err := bolt.Open(filepath.Join(w.root, "containerdmeta.db"), 0644, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open metadata db: %w", err)
	}

	mdb := ctdmetadata.NewDB(db, localstore, map[string]ctdsnapshot.Snapshotter{
		opts.SnapshotterFactory.Name: snapshotter,
	})
	if err := mdb.Init(context.TODO()); err != nil {
		return nil, fmt.Errorf("failed to init metadata db: %w", err)
	}

	lm := leaseutil.WithNamespace(ctdmetadata.NewLeaseManager(mdb), "buildkit")
	snap := containerdsnapshot.NewSnapshotter(
		opts.SnapshotterFactory.Name,
		mdb.Snapshotter(opts.SnapshotterFactory.Name),
		"buildkit",
		opts.IDMapping,
	)
	md, err := metadata.NewStore(filepath.Join(w.root, "metadata_v2.db"))
	if err != nil {
		return nil, fmt.Errorf("failed to create metadata store: %w", err)
	}

	contentStore := containerdsnapshot.NewContentStore(mdb.ContentStore(), "buildkit")

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	baseLabels := map[string]string{
		wlabel.Executor:       "oci",
		wlabel.Snapshotter:    opts.SnapshotterFactory.Name,
		wlabel.Hostname:       hostname,
		wlabel.Network:        npResolvedMode,
		wlabel.OCIProcessMode: opts.ProcessMode.String(),
		wlabel.SELinuxEnabled: strconv.FormatBool(opts.SELinux),
	}
	if opts.ApparmorProfile != "" {
		baseLabels[wlabel.ApparmorProfile] = opts.ApparmorProfile
	}
	for k, v := range opts.Labels {
		baseLabels[k] = v
	}

	id, err := base.ID(w.root)
	if err != nil {
		return nil, fmt.Errorf("failed to get worker ID: %w", err)
	}

	if len(opts.Platforms) == 0 {
		opts.Platforms = []ocispecs.Platform{platforms.Normalize(platforms.DefaultSpec())}
	}

	w.Worker, err = base.NewWorker(ctx, base.WorkerOpt{
		ID:               id,
		Labels:           baseLabels,
		Platforms:        opts.Platforms,
		GCPolicy:         opts.GCPolicy,
		BuildkitVersion:  opts.BuildkitVersion,
		NetworkProviders: w.networkProviders,
		Executor:         nil, // handled in this Worker struct
		Snapshotter:      snap,
		ContentStore:     contentStore,
		Applier:          winlayers.NewFileSystemApplierWithWindows(contentStore, apply.NewFileSystemApplier(contentStore)),
		Differ:           winlayers.NewWalkingDiffWithWindows(contentStore, walking.NewWalkingDiff(contentStore)),
		ImageStore:       nil, // explicitly, because that's what upstream does too
		RegistryHosts:    opts.RegistryHosts,
		IdentityMapping:  opts.IDMapping,
		LeaseManager:     lm,
		GarbageCollect:   mdb.GarbageCollect,
		ParallelismSem:   opts.ParallelismSem,
		MetadataStore:    md,
		MountPoolRoot:    filepath.Join(w.root, "cachemounts"),
		ResourceMonitor:  nil, // we don't use it
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create base worker: %w", err)
	}

	if err := w.registerDaggerCustomSources(); err != nil {
		return nil, fmt.Errorf("failed to register dagger custom sources: %w", err)
	}

	return w, nil
}

// registerDaggerCustomSources adds Dagger's custom sources to the worker.
func (w *Worker) registerDaggerCustomSources() error {
	hs, err := httpdns.NewSource(httpdns.Opt{
		Opt: srchttp.Opt{
			CacheAccessor: w.CacheMgr,
		},
		BaseDNSConfig: w.dns,
	})
	if err != nil {
		return err
	}

	w.SourceManager.Register(hs)

	gs, err := gitdns.NewSource(gitdns.Opt{
		Opt: srcgit.Opt{
			CacheAccessor: w.CacheMgr,
		},
		BaseDNSConfig: w.dns,
	})
	if err != nil {
		return err
	}

	w.SourceManager.Register(gs)

	bs, err := blob.NewSource(blob.Opt{
		CacheAccessor: w.CacheMgr,
	})
	if err != nil {
		return err
	}

	w.SourceManager.Register(bs)

	return nil
}

/*
Buildkit's worker.Controller is a bit odd; it exists to manage multiple workers because that was
a planned feature years ago, but it never got implemented. So it exists to manage a single worker,
which doesn't really add much.

We still need to provide a worker.Controller value to a few places though, which this method enables.
*/
func (w *Worker) AsWorkerController() (*worker.Controller, error) {
	wc := &worker.Controller{}
	err := wc.Add(w)
	if err != nil {
		return nil, err
	}
	return wc, nil
}

func (w *Worker) Executor() executor.Executor {
	return w
}

func (w *Worker) ResolveOp(vtx solver.Vertex, s frontend.FrontendLLBBridge, sm *session.Manager) (solver.Op, error) {
	// if this is an ExecOp, pass in ourself as executor
	if baseOp, ok := vtx.Sys().(*pb.Op); ok {
		if execOp, ok := baseOp.Op.(*pb.Op_Exec); ok {
			execMD, ok, err := executionMetadataFromVtx(vtx)
			if err != nil {
				return nil, err
			}
			if ok {
				w = w.withExecMD(*execMD)
			}
			return ops.NewExecOp(
				vtx,
				execOp,
				baseOp.Platform,
				w.CacheMgr,
				w.ParallelismSem,
				sm,
				w, // executor
				w,
			)
		}
	}

	// otherwise, just use the default base.Worker's ResolveOp
	return w.Worker.ResolveOp(vtx, s, sm)
}

func (w *Worker) withExecMD(execMD ExecutionMetadata) *Worker {
	return &Worker{sharedWorkerState: w.sharedWorkerState, execMD: &execMD}
}
