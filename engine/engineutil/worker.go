package engineutil

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	runc "github.com/containerd/go-runc"
	"github.com/dagger/dagger/dagql"
	imageexport "github.com/dagger/dagger/engine/engineutil/imageexport"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	containerdsnapshot "github.com/dagger/dagger/engine/snapshots/containerd"
	snapshot "github.com/dagger/dagger/engine/snapshots/snapshotter"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/util/entitlements"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	"github.com/dagger/dagger/internal/buildkit/util/network"
	"github.com/docker/docker/pkg/idtools"
	"github.com/hashicorp/go-multierror"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/semaphore"
)

/*
Worker is Dagger's custom worker.

We need a custom Executor implementation for setting up containers (currently, just
for providing SessionID, but in the future everything the shim does will be migrated
here). For simplicity, this Worker struct also implements that Executor interface
(in executor.go) since Worker+Executor are so tightly bound together anyways.
*/
type Worker struct {
	*sharedWorkerState
	causeCtx trace.SpanContext
	execMD   *ExecutionMetadata
}

type sharedWorkerState struct {
	WorkerOpt
	root              string
	executorRoot      string
	telemetryPubSub   http.Handler
	bkSessionManager  *bksession.Manager
	sessionHandler    sessionHandler
	dagqlServer       dagqlServer
	imageExportWriter *imageexport.Writer

	runc             *runc.Runc
	cgroupParent     string
	networkProviders map[pb.NetMode]network.Provider
	processMode      oci.ProcessMode
	idmap            *idtools.IdentityMapping
	dns              *oci.DNSConfig
	apparmorProfile  string
	selinux          bool
	entitlements     entitlements.Set
	parallelismSem   *semaphore.Weighted
	workerCache      bkcache.SnapshotManager

	hostMntNS  *os.File
	cleanMntNS *os.File

	running map[string]*execState
	mu      sync.RWMutex
}

// WorkerOpt is specific to a worker.
type WorkerOpt struct {
	ID               string
	Root             string
	Labels           map[string]string
	Platforms        []ocispecs.Platform
	GCPolicy         []bkclient.PruneInfo
	BuildkitVersion  bkclient.BuildkitVersion
	NetworkProviders map[pb.NetMode]network.Provider
	Snapshotter      snapshot.Snapshotter
	ContentStore     *containerdsnapshot.Store
	Applier          diff.Applier
	Differ           diff.Comparer
	ImageStore       images.Store // optional
	RegistryHosts    docker.RegistryHosts
	IdentityMapping  *idtools.IdentityMapping
	LeaseManager     *leaseutil.Manager
}

type sessionHandler interface {
	ServeHTTPToNestedClient(http.ResponseWriter, *http.Request, *ExecutionMetadata)
}

type dagqlServer interface {
	Server(ctx context.Context) (*dagql.Server, error)
}

type NewWorkerOpts struct {
	WorkerOpt
	WorkerRoot       string
	ExecutorRoot     string
	TelemetryPubSub  http.Handler
	BKSessionManager *bksession.Manager
	SessionHandler   sessionHandler
	DagqlServer      dagqlServer

	Runc                *runc.Runc
	DefaultCgroupParent string
	ProcessMode         oci.ProcessMode
	DNSConfig           *oci.DNSConfig
	ApparmorProfile     string
	SELinux             bool
	Entitlements        entitlements.Set
	ParallelismSem      *semaphore.Weighted
	WorkerCache         bkcache.SnapshotManager

	HostMntNS  *os.File
	CleanMntNS *os.File
}

func NewWorker(opts *NewWorkerOpts) (*Worker, error) {
	imageWriter, err := imageexport.NewWriter(imageexport.WriterOpt{
		Snapshotter:  opts.Snapshotter,
		ContentStore: opts.ContentStore,
		Applier:      opts.Applier,
		Differ:       opts.Differ,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create image writer: %w", err)
	}

	return &Worker{sharedWorkerState: &sharedWorkerState{
		WorkerOpt:         opts.WorkerOpt,
		root:              opts.WorkerRoot,
		executorRoot:      opts.ExecutorRoot,
		telemetryPubSub:   opts.TelemetryPubSub,
		bkSessionManager:  opts.BKSessionManager,
		sessionHandler:    opts.SessionHandler,
		dagqlServer:       opts.DagqlServer,
		imageExportWriter: imageWriter,

		runc:             opts.Runc,
		cgroupParent:     opts.DefaultCgroupParent,
		networkProviders: opts.WorkerOpt.NetworkProviders,
		processMode:      opts.ProcessMode,
		idmap:            opts.WorkerOpt.IdentityMapping,
		dns:              opts.DNSConfig,
		apparmorProfile:  opts.ApparmorProfile,
		selinux:          opts.SELinux,
		entitlements:     opts.Entitlements,
		parallelismSem:   opts.ParallelismSem,
		workerCache:      opts.WorkerCache,

		hostMntNS:  opts.HostMntNS,
		cleanMntNS: opts.CleanMntNS,

		running: make(map[string]*execState),
	}}, nil
}

func (w *Worker) ExecWorker(causeCtx trace.SpanContext, execMD ExecutionMetadata) *Worker {
	return &Worker{sharedWorkerState: w.sharedWorkerState, causeCtx: causeCtx, execMD: &execMD}
}

func (w *Worker) Close() error {
	var rerr error
	for _, provider := range w.NetworkProviders {
		if err := provider.Close(); err != nil {
			rerr = multierror.Append(rerr, err)
		}
	}
	return rerr
}

func (w *Worker) ContentStore() *containerdsnapshot.Store {
	return w.WorkerOpt.ContentStore
}

func (w *Worker) LeaseManager() *leaseutil.Manager {
	return w.WorkerOpt.LeaseManager
}

func (w *Worker) ID() string {
	return w.WorkerOpt.ID
}

func (w *Worker) Labels() map[string]string {
	return w.WorkerOpt.Labels
}

func (w *Worker) Platforms(_ bool) []ocispecs.Platform {
	return w.WorkerOpt.Platforms
}

func (w *Worker) BuildkitVersion() bkclient.BuildkitVersion {
	return w.WorkerOpt.BuildkitVersion
}

func (w *Worker) GCPolicy() []bkclient.PruneInfo {
	return w.WorkerOpt.GCPolicy
}

func (w *Worker) DiskUsage(ctx context.Context, opt bkclient.DiskUsageInfo) ([]*bkclient.UsageInfo, error) {
	return nil, nil
}

func (w *Worker) Prune(ctx context.Context, ch chan bkclient.UsageInfo, opt ...bkclient.PruneInfo) error {
	return nil
}
