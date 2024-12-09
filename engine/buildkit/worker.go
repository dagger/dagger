package buildkit

import (
	"context"
	"errors"
	"net/http"
	"sync"

	runc "github.com/containerd/go-runc"
	"github.com/dagger/dagger/engine/sources/containerimagedns"
	"github.com/docker/docker/pkg/idtools"
	bkcache "github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/executor"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/frontend"
	"github.com/moby/buildkit/session"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/llbsolver/ops"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/source/containerimage"
	"github.com/moby/buildkit/util/entitlements"
	"github.com/moby/buildkit/util/network"
	"github.com/moby/buildkit/worker"
	"github.com/moby/buildkit/worker/base"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/semaphore"
)

/*
Worker is Dagger's custom worker. Most of the buildkit Worker interface methods are
just inherited from buildkit's base.Worker, with the exception of methods involving
executor.Executor (most importantly ResolveOp).

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
	*base.Worker
	root             string
	executorRoot     string
	telemetryPubSub  http.Handler
	bkSessionManager *bksession.Manager
	sessionHandler   sessionHandler

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
	workerCache      bkcache.Manager
	dnsImageSource   *containerimagedns.Source

	running map[string]*execState
	mu      sync.RWMutex
}

type sessionHandler interface {
	ServeHTTPToNestedClient(http.ResponseWriter, *http.Request, *ExecutionMetadata)
}

type NewWorkerOpts struct {
	WorkerRoot       string
	ExecutorRoot     string
	BaseWorker       *base.Worker
	TelemetryPubSub  http.Handler
	BKSessionManager *bksession.Manager
	SessionHandler   sessionHandler

	Runc                *runc.Runc
	DefaultCgroupParent string
	ProcessMode         oci.ProcessMode
	IDMapping           *idtools.IdentityMapping
	DNSConfig           *oci.DNSConfig
	ApparmorProfile     string
	SELinux             bool
	Entitlements        entitlements.Set
	NetworkProviders    map[pb.NetMode]network.Provider
	ParallelismSem      *semaphore.Weighted
	WorkerCache         bkcache.Manager
	DNSImageSource      *containerimagedns.Source
}

func NewWorker(opts *NewWorkerOpts) *Worker {
	return &Worker{sharedWorkerState: &sharedWorkerState{
		Worker:           opts.BaseWorker,
		root:             opts.WorkerRoot,
		executorRoot:     opts.ExecutorRoot,
		telemetryPubSub:  opts.TelemetryPubSub,
		bkSessionManager: opts.BKSessionManager,
		sessionHandler:   opts.SessionHandler,

		runc:             opts.Runc,
		cgroupParent:     opts.DefaultCgroupParent,
		networkProviders: opts.NetworkProviders,
		processMode:      opts.ProcessMode,
		idmap:            opts.IDMapping,
		dns:              opts.DNSConfig,
		apparmorProfile:  opts.ApparmorProfile,
		selinux:          opts.SELinux,
		entitlements:     opts.Entitlements,
		parallelismSem:   opts.ParallelismSem,
		workerCache:      opts.WorkerCache,
		dnsImageSource:   opts.DNSImageSource,

		running: make(map[string]*execState),
	}}
}

func (w *Worker) Executor() executor.Executor {
	return w
}

func (w *Worker) ResolveOp(vtx solver.Vertex, s frontend.FrontendLLBBridge, sm *bksession.Manager) (solver.Op, error) {
	// if this is an ExecOp, pass in ourself as executor
	if baseOp, ok := vtx.Sys().(*pb.Op); ok {
		if execOp, ok := baseOp.Op.(*pb.Op_Exec); ok {
			execMD, ok, err := executionMetadataFromVtx(vtx)
			if err != nil {
				return nil, err
			}
			if ok {
				w = w.execWorker(
					SpanContextFromDescription(vtx.Options().Description),
					*execMD,
				)
			}
			return ops.NewExecOp(
				vtx,
				execOp,
				baseOp.Platform,
				w.workerCache,
				w.parallelismSem,
				sm,
				w, // executor
				w,
			)
		}
	}

	// otherwise, just use the default base.Worker's ResolveOp
	return w.Worker.ResolveOp(vtx, s, sm)
}

func (w *Worker) ResolveSourceMetadata(ctx context.Context, op *pb.SourceOp, opt sourceresolver.Opt, sm *session.Manager, g session.Group) (*sourceresolver.MetaResponse, error) {
	if opt.SourcePolicies != nil {
		return nil, errors.New("source policies can not be set for worker")
	}

	var platform *pb.Platform
	if p := opt.Platform; p != nil {
		platform = &pb.Platform{
			Architecture: p.Architecture,
			OS:           p.OS,
			Variant:      p.Variant,
			OSVersion:    p.OSVersion,
		}
	}

	id, err := w.SourceManager.Identifier(&pb.Op_Source{Source: op}, platform)
	if err != nil {
		return nil, err
	}

	if imgid, ok := id.(containerimagedns.Identifier); ok {
		switch idt := imgid.Identifier.(type) {
		case *containerimage.ImageIdentifier:
			if opt.ImageOpt == nil {
				opt.ImageOpt = &sourceresolver.ResolveImageOpt{}
			}
			dgst, config, err := w.dnsImageSource.Source.ResolveImageConfig(ctx, idt.Reference.String(), opt, sm, g)
			if err != nil {
				return nil, err
			}
			return &sourceresolver.MetaResponse{
				Op: op,
				Image: &sourceresolver.ResolveImageResponse{
					Digest: dgst,
					Config: config,
				},
			}, nil
		case *containerimage.OCIIdentifier:
			opt.OCILayoutOpt = &sourceresolver.ResolveOCILayoutOpt{
				Store: sourceresolver.ResolveImageConfigOptStore{
					StoreID:   idt.StoreID,
					SessionID: idt.SessionID,
				},
			}
			dgst, config, err := w.OCILayoutSource.ResolveImageConfig(ctx, idt.Reference.String(), opt, sm, g)
			if err != nil {
				return nil, err
			}
			return &sourceresolver.MetaResponse{
				Op: op,
				Image: &sourceresolver.ResolveImageResponse{
					Digest: dgst,
					Config: config,
				},
			}, nil
		}
	}

	return &sourceresolver.MetaResponse{
		Op: op,
	}, nil
}

func (w *Worker) execWorker(causeCtx trace.SpanContext, execMD ExecutionMetadata) *Worker {
	return &Worker{sharedWorkerState: w.sharedWorkerState, causeCtx: causeCtx, execMD: &execMD}
}

/*
Buildkit's worker.Controller is a bit odd; it exists to manage multiple workers because that was
a planned feature years ago, but it never got implemented. So it exists to manage a single worker,
which doesn't really add much.

We still need to provide a worker.Controller value to a few places though, which this method enables.
*/
func AsWorkerController(w worker.Worker) (*worker.Controller, error) {
	wc := &worker.Controller{}
	err := wc.Add(w)
	if err != nil {
		return nil, err
	}
	return wc, nil
}
