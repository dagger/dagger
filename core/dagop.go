package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver"
	"github.com/opencontainers/go-digest"
)

type DagOp struct {
	ID *call.ID
}

func DagLLB(ctx context.Context, srv *dagql.Server, sels []dagql.Selector, inputs []llb.State) (llb.State, error) {
	id, err := srv.SelectID(ctx, srv.Root(), sels...)
	if err != nil {
		return llb.State{}, err
	}
	op := DagOp{ID: id}
	return buildkit.NewCustomLLB(ctx, op, inputs, llb.WithCustomName("dagop"), buildkit.WithPassthrough())
}

func (op DagOp) Name() string {
	return "dagop"
}

func (op DagOp) Backend() buildkit.CustomOpBackend {
	return &op
}

func (op DagOp) CacheKey(ctx context.Context) (key digest.Digest, err error) {
	enc, err := op.ID.Encode()
	if err != nil {
		return key, err
	}
	return digest.FromString(enc), nil
}

type dagOpContextKey struct{}

func withDagOpContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, dagOpContextKey{}, true)
}

func IsDagOp(ctx context.Context) bool {
	if val := ctx.Value(dagOpContextKey{}); val != nil {
		return val.(bool)
	}
	return false
}

func (op DagOp) Exec(ctx context.Context, inputs []solver.Result, srv *dagql.Server) (outputs []solver.Result, err error) {
	obj, err := srv.Load(withDagOpContext(ctx), op.ID)
	if err != nil {
		return nil, err
	}
	inst, ok := obj.(dagql.Instance[*Directory])
	if !ok {
		return nil, fmt.Errorf("bad, not really a directory")
	}

	if inst.Self.Dir != "" && inst.Self.Dir != "/" {
		return nil, fmt.Errorf("directory %q is not root", inst.Self.Dir)
	}

	res, err := inst.Self.Evaluate(ctx)
	if err != nil {
		return nil, err
	}

	ref, err := res.Ref.Result(ctx)
	if err != nil {
		return nil, err
	}
	return []solver.Result{ref}, nil
}

func init() {
	buildkit.RegisterCustomOp(DagOp{})
}
