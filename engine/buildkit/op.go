package buildkit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/trace"
)

type CustomOpWrapper struct {
	Name    string
	Backend CustomOpBackend

	ClientMetadata engine.ClientMetadata

	causeCtx       trace.SpanContext
	server         dagqlServer
	original       solver.Op
	worker         worker.Worker
	sessionManager *bksession.Manager
}

type CustomOp interface {
	Name() string
	Backend() CustomOpBackend
}

type CustomOpBackend interface {
	Digest() (digest.Digest, error)
	CacheMap(ctx context.Context, cm *solver.CacheMap) (*solver.CacheMap, error)
	Exec(ctx context.Context, g bksession.Group, inputs []solver.Result, opts OpOpts) (outputs []solver.Result, err error)
}

type OpOpts struct {
	CauseCtx trace.SpanContext
	Server   *dagql.Server
	Worker   worker.Worker
}

var customOps = map[string]CustomOp{}

func RegisterCustomOp(op CustomOp) {
	customOps[op.Name()] = op
}

func NewCustomLLB(ctx context.Context, op CustomOp, inputs []llb.State, opts ...llb.ConstraintsOpt) (llb.State, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return llb.State{}, fmt.Errorf("failed to get client metadata: %w", err)
	}

	opWrapped := CustomOpWrapper{
		Name:           op.Name(),
		Backend:        op.Backend(),
		ClientMetadata: *clientMetadata,
	}

	// generate a uniqued digest of the op to use in the buildkit id (this
	// prevents all our ops merging together in the solver)
	id, err := opWrapped.Digest()
	if err != nil {
		return llb.State{}, err
	}

	// pre-populate a reasonable underlying representation that has some inputs
	a := llb.Rm("/" + id.Encoded())
	for _, input := range inputs {
		a = a.Copy(input, "/", "/")
	}
	st := llb.Scratch().File(a)
	customOpOpt, err := opWrapped.AsConstraintsOpt()
	if err != nil {
		return llb.State{}, fmt.Errorf("constraints opt: %w", err)
	}

	marshalOpts := append([]llb.ConstraintsOpt{customOpOpt}, opts...)
	def, err := st.Marshal(ctx, marshalOpts...)
	if err != nil {
		return llb.State{}, fmt.Errorf("marshal root: %w", err)
	}

	f, err := llb.NewDefinitionOp(def.ToPB())
	if err != nil {
		return llb.State{}, err
	}
	return llb.NewState(f), nil
}

func (op *CustomOpWrapper) CacheMap(ctx context.Context, g bksession.Group, index int) (*solver.CacheMap, bool, error) {
	cm, ok, err := op.original.CacheMap(ctx, g, index)
	if cm == nil || !ok || err != nil {
		return cm, ok, err
	}

	clientMetadata, err := op.clientMetadata(ctx, g)
	if err != nil {
		return nil, false, err
	}

	ctx = engine.ContextWithClientMetadata(ctx, clientMetadata)
	cm, err = op.Backend.CacheMap(ctx, cm)
	if err != nil {
		return nil, false, err
	}
	return cm, true, nil
}

type bkSessionGroupContextKey struct{}

func ctxWithBkSessionGroup(ctx context.Context, g bksession.Group) context.Context {
	return context.WithValue(ctx, bkSessionGroupContextKey{}, g)
}

func CurrentBuildkitSessionGroup(ctx context.Context) (bksession.Group, bool) {
	g, ok := ctx.Value(bkSessionGroupContextKey{}).(bksession.Group)
	return g, ok
}

func (op *CustomOpWrapper) Exec(ctx context.Context, g bksession.Group, inputs []solver.Result) (outputs []solver.Result, err error) {
	clientMetadata, err := op.clientMetadata(ctx, g)
	if err != nil {
		return nil, err
	}
	ctx = engine.ContextWithClientMetadata(ctx, clientMetadata)
	ctx = ctxWithBkSessionGroup(ctx, g)

	server, err := op.server.Server(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not find dagql server: %w", err)
	}

	res, err := op.Backend.Exec(ctx, g, inputs, OpOpts{
		CauseCtx: op.causeCtx,
		Server:   server,
		Worker:   op.worker,
	})
	return res, err
}

func (op *CustomOpWrapper) Acquire(ctx context.Context) (release solver.ReleaseFunc, err error) {
	return op.original.Acquire(ctx)
}

func (op *CustomOpWrapper) clientMetadata(ctx context.Context, g bksession.Group) (md *engine.ClientMetadata, _ error) {
	_, err := op.server.Server(engine.ContextWithClientMetadata(ctx, &op.ClientMetadata))
	if err == nil {
		return &op.ClientMetadata, nil
	}

	err = op.sessionManager.Any(ctx, g, func(ctx context.Context, id string, c bksession.Caller) error {
		var err error
		md, err = engine.ClientMetadataFromContext(c.Context())
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if md == nil {
		return nil, fmt.Errorf("no client metadata found in available sessions")
	}
	return md, nil
}

const customOpKey = "dagger.customOp"

func (w *Worker) customOpFromVtx(vtx solver.Vertex, s frontend.FrontendLLBBridge, sm *bksession.Manager) (solver.Op, bool, error) {
	if vtx == nil {
		return nil, false, nil
	}
	customOp, ok, err := customOpFromDescription(vtx.Options().Description)
	if err != nil {
		return customOp, ok, err
	}
	if customOp != nil {
		op, err := w.Worker.ResolveOp(vtx, s, sm)
		if err != nil {
			return customOp, ok, err
		}
		customOp.causeCtx = SpanContextFromDescription(vtx.Options().Description)
		customOp.original = op
		customOp.server = w.dagqlServer
		customOp.worker = w
		customOp.sessionManager = w.bkSessionManager
	}
	return customOp, ok, nil
}

func customOpFromDescription(desc map[string]string) (*CustomOpWrapper, bool, error) {
	if desc == nil {
		return nil, false, nil
	}

	bs, ok := desc[customOpKey]
	if !ok {
		return nil, false, nil
	}

	wrapper := struct {
		Backend json.RawMessage
		CustomOpWrapper
	}{}
	if err := json.Unmarshal([]byte(bs), &wrapper); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal custom op: %w", err)
	}

	op, ok := customOps[wrapper.Name]
	if !ok {
		return nil, false, fmt.Errorf("no custom op %q", wrapper.Name)
	}
	wrapper.CustomOpWrapper.Backend = op.Backend()
	if err := json.Unmarshal(wrapper.Backend, &wrapper.CustomOpWrapper.Backend); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal custom op %q: %w", wrapper.Name, err)
	}
	return &wrapper.CustomOpWrapper, true, nil
}

func (op CustomOpWrapper) AsConstraintsOpt() (llb.ConstraintsOpt, error) {
	bs, err := json.Marshal(op)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal custom op: %w", err)
	}
	return llb.WithDescription(map[string]string{
		customOpKey: string(bs),
	}), nil
}

func (op CustomOpWrapper) Digest() (digest.Digest, error) {
	dgst, err := op.Backend.Digest()
	if err != nil {
		return "", err
	}
	return digest.FromString(op.Name + ":" + string(dgst)), nil
}
