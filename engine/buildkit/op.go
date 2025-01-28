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
	"github.com/opencontainers/go-digest"
)

type CustomOpWrapper struct {
	Name    string
	Backend CustomOpBackend

	ClientMetadata engine.ClientMetadata

	server   dagqlServer
	original solver.Op
}

type CustomOp interface {
	Name() string
	Backend() CustomOpBackend
}

type CustomOpBackend interface {
	CacheKey(ctx context.Context) (digest.Digest, error)
	Exec(ctx context.Context, inputs []solver.Result, server *dagql.Server) (outputs []solver.Result, err error)
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

	// pre-populate a reasonable underlying representation that has some inputs
	var a *llb.FileAction
	a = llb.Rm("/")
	for _, input := range inputs {
		if a == nil {
			a = llb.Copy(input, "/", "/")
		} else {
			a = a.Copy(input, "/", "/")
		}
	}
	st := llb.Scratch()
	if a != nil {
		st = st.File(a)
	}
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
	if err != nil {
		return cm, ok, err
	}
	if cm != nil {
		key, err := op.Backend.CacheKey(ctx)
		if err != nil {
			return cm, ok, err
		}
		cm.Digest = digest.FromString(string(cm.Digest) + "+" + string(key))
	}
	return cm, ok, err
}

func (op *CustomOpWrapper) Exec(ctx context.Context, g bksession.Group, inputs []solver.Result) (outputs []solver.Result, err error) {
	ctx = engine.ContextWithClientMetadata(ctx, &op.ClientMetadata)

	server, err := op.server.DagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not find dagql server: %w", err)
	}

	return op.Backend.Exec(ctx, inputs, server)
}

func (op *CustomOpWrapper) Acquire(ctx context.Context) (release solver.ReleaseFunc, err error) {
	return op.original.Acquire(ctx)
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
		customOp.original = op
		customOp.server = w.dagqlServer
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
