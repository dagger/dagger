package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
	bkcache "github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/snapshot"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
)

func init() {
	buildkit.RegisterCustomOp(FSDagOp{})
	buildkit.RegisterCustomOp(RawDagOp{})
}

// NewDirectoryDagOp takes a target ID for a Directory, and returns a Directory
// for it, computing the actual dagql query inside a buildkit operation, which
// allows for efficiently caching the result.
func NewDirectoryDagOp(
	ctx context.Context,
	srv *dagql.Server,
	dagop *FSDagOp,
	inputs []llb.State,
) (*Directory, error) {
	st, err := newFSDagOp[*Directory](ctx, dagop, inputs)
	if err != nil {
		return nil, err
	}
	query, ok := srv.Root().(dagql.Instance[*Query])
	if !ok {
		return nil, fmt.Errorf("server root was %T", srv.Root())
	}
	return NewDirectorySt(ctx, query.Self, st, dagop.Path, query.Self.Platform(), nil)
}

// NewFileDagOp takes a target ID for a File, and returns a File for it,
// computing the actual dagql query inside a buildkit operation, which allows
// for efficiently caching the result.
func NewFileDagOp(
	ctx context.Context,
	srv *dagql.Server,
	dagop *FSDagOp,
	inputs []llb.State,
) (*File, error) {
	st, err := newFSDagOp[*File](ctx, dagop, inputs)
	if err != nil {
		return nil, err
	}
	query, ok := srv.Root().(dagql.Instance[*Query])
	if !ok {
		return nil, fmt.Errorf("server root was %T", srv.Root())
	}
	return NewFileSt(ctx, query.Self, st, dagop.Path, query.Self.Platform(), nil)
}

func newFSDagOp[T dagql.Typed](
	ctx context.Context,
	dagop *FSDagOp,
	inputs []llb.State,
) (llb.State, error) {
	if dagop.ID == nil {
		return llb.State{}, fmt.Errorf("dagop ID is nil")
	}

	var t T
	requiredType := t.Type().NamedType
	if dagop.ID.Type().NamedType() != requiredType {
		return llb.State{}, fmt.Errorf("expected %s to be selected, instead got %s", requiredType, dagop.ID.Type().NamedType())
	}

	return newDagOpLLB(ctx, dagop, dagop.ID, inputs)
}

type FSDagOp struct {
	ID *call.ID

	// Path is the target path for the output - this is mostly ignored by dagop
	// (except for contributing to the cache key). However, it can be used by
	// dagql running inside a dagop to determine where it should write data.
	Path string

	// Data is any additional data that should be passed to the dagop. It does
	// not contribute to the cache key.
	Data any

	// utility values set in the context of an Exec
	g   bksession.Group
	opt buildkit.OpOpts
}

func (op FSDagOp) Name() string {
	return "dagop.fs"
}

func (op FSDagOp) Backend() buildkit.CustomOpBackend {
	return &op
}

func (op FSDagOp) Digest() (digest.Digest, error) {
	return digest.FromString(strings.Join([]string{
		op.ID.Digest().String(),
		op.Path,
	}, "+")), nil
}
func (op FSDagOp) CacheKey(ctx context.Context) (key digest.Digest, err error) {
	return digest.FromString(strings.Join([]string{
		op.ID.Digest().String(),
		op.Path,
	}, "+")), nil
}

func (op FSDagOp) Exec(ctx context.Context, g bksession.Group, inputs []solver.Result, opt buildkit.OpOpts) (outputs []solver.Result, err error) {
	op.g = g
	op.opt = opt
	obj, err := opt.Server.Load(withDagOpContext(ctx, op), op.ID)
	if err != nil {
		return nil, err
	}

	switch inst := obj.(type) {
	case dagql.Instance[*Directory]:
		if inst.Self.Result != nil {
			ref := worker.NewWorkerRefResult(inst.Self.Result.Clone(), opt.Worker)
			return []solver.Result{ref}, nil
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

	case dagql.Instance[*File]:
		if inst.Self.Result != nil {
			ref := worker.NewWorkerRefResult(inst.Self.Result.Clone(), opt.Worker)
			return []solver.Result{ref}, nil
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

	default:
		// shouldn't happen, should have errored in DagLLB already
		return nil, fmt.Errorf("expected FS to be selected, instead got %T", obj)
	}
}

func (op FSDagOp) Group() bksession.Group {
	return op.g
}

func (op FSDagOp) Mount(ctx context.Context, ref bkcache.Ref, f func(string) error) error {
	return MountRef(ctx, ref, op.g, f)
}

// NewRawDagOp takes a target ID for any JSON-serializable dagql type, and returns
// it, computing the actual dagql query inside a buildkit operation, which
// allows for efficiently caching the result.
func NewRawDagOp[T dagql.Typed](
	ctx context.Context,
	srv *dagql.Server,
	dagop *RawDagOp,
	inputs []llb.State,
) (t T, err error) {
	if dagop.ID == nil {
		return t, fmt.Errorf("dagop ID is nil")
	}
	if dagop.Filename == "" {
		return t, fmt.Errorf("dagop filename is empty")
	}

	st, err := newDagOpLLB(ctx, dagop, dagop.ID, inputs)
	if err != nil {
		return t, err
	}

	query, ok := srv.Root().(dagql.Instance[*Query])
	if !ok {
		return t, fmt.Errorf("server root was %T", srv.Root())
	}

	f, err := NewFileSt(ctx, query.Self, st, dagop.Filename, Platform{}, nil)
	if err != nil {
		return t, err
	}
	dt, err := f.Contents(ctx)
	if err != nil {
		return t, err
	}
	err = json.Unmarshal(dt, &t)
	return t, err
}

type RawDagOp struct {
	ID       *call.ID
	Filename string
}

func (op RawDagOp) Name() string {
	return "dagop.raw"
}

func (op RawDagOp) Backend() buildkit.CustomOpBackend {
	return &op
}

func (op RawDagOp) Digest() (digest.Digest, error) {
	return digest.FromString(strings.Join([]string{
		op.ID.Digest().String(),
		op.Filename,
	}, "+")), nil
}

func (op RawDagOp) CacheKey(ctx context.Context) (key digest.Digest, err error) {
	return digest.FromString(strings.Join([]string{
		op.ID.Digest().String(),
		op.Filename,
	}, "+")), nil
}

func (op RawDagOp) Exec(ctx context.Context, g bksession.Group, inputs []solver.Result, opt buildkit.OpOpts) (outputs []solver.Result, retErr error) {
	result, err := opt.Server.LoadType(withDagOpContext(ctx, op), op.ID)
	if err != nil {
		return nil, err
	}
	if wrapped, ok := result.(dagql.Wrapper); ok {
		result = wrapped.Unwrap()
	}

	query, ok := opt.Server.Root().(dagql.Instance[*Query])
	if !ok {
		return nil, fmt.Errorf("server root was %T", opt.Server.Root())
	}
	ref, err := query.Self.BuildkitCache().New(ctx, nil, g,
		bkcache.CachePolicyRetain,
		bkcache.WithRecordType(client.UsageRecordTypeRegular),
		bkcache.WithDescription(op.Name()))
	if err != nil {
		return nil, fmt.Errorf("failed to create new mutable: %w", err)
	}
	defer func() {
		if retErr != nil && ref != nil {
			ref.Release(context.WithoutCancel(ctx))
		}
	}()

	mount, err := ref.Mount(ctx, false, g)
	if err != nil {
		return nil, err
	}
	lm := snapshot.LocalMounter(mount)
	dir, err := lm.Mount()
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil && lm != nil {
			lm.Unmount()
		}
	}()

	f, err := os.Create(filepath.Join(dir, op.Filename))
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil && f != nil {
			f.Close()
		}
	}()

	enc := json.NewEncoder(f)
	err = enc.Encode(result)
	if err != nil {
		return nil, err
	}
	err = f.Close()
	if err != nil {
		return nil, err
	}
	f = nil

	lm.Unmount()
	lm = nil

	snap, err := ref.Commit(ctx)
	if err != nil {
		return nil, err
	}
	ref = nil

	return []solver.Result{worker.NewWorkerRefResult(snap, opt.Worker)}, nil
}

func newDagOpLLB(ctx context.Context, dagOp buildkit.CustomOp, id *call.ID, inputs []llb.State) (llb.State, error) {
	return buildkit.NewCustomLLB(ctx, dagOp, inputs,
		llb.WithCustomNamef("%s %s", dagOp.Name(), id.Name()),
		buildkit.WithTracePropagation(ctx),
		buildkit.WithPassthrough(),
	)
}

type dagOpContextKey string

func withDagOpContext(ctx context.Context, op buildkit.CustomOp) context.Context {
	return context.WithValue(ctx, dagOpContextKey(op.Name()), op)
}

func DagOpFromContext[T buildkit.CustomOp](ctx context.Context) (t T, ok bool) {
	if val := ctx.Value(dagOpContextKey(t.Name())); val != nil {
		t, ok = val.(T)
	}
	return t, ok
}

func DagOpInContext[T buildkit.CustomOp](ctx context.Context) bool {
	_, ok := DagOpFromContext[T](ctx)
	return ok
}

// MountRef is a utility for easily mounting a ref
func MountRef(ctx context.Context, ref bkcache.Ref, g bksession.Group, f func(string) error) error {
	mount, err := ref.Mount(ctx, false, g)
	if err != nil {
		return err
	}
	lm := snapshot.LocalMounter(mount)
	defer lm.Unmount()

	dir, err := lm.Mount()
	if err != nil {
		return err
	}
	return f(dir)
}
