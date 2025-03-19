package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/moby/buildkit/client/llb"
)

// DagOpWrapper caches an arbitrary dagql field as a buildkit operation
func DagOpWrapper[T dagql.Typed, A any, R dagql.Typed](
	srv *dagql.Server,
	fn dagql.NodeFuncHandler[T, A, R],
) dagql.NodeFuncHandler[T, A, R] {
	return func(ctx context.Context, self dagql.Instance[T], args A) (inst R, err error) {
		if core.DagOpInContext[core.RawDagOp](ctx) {
			return fn(ctx, self, args)
		}
		return DagOp(ctx, srv, self, args, fn)
	}
}

// DagOp creates a RawDagOp from an arbitrary operation
//
// NOTE: prefer DagOpWrapper where possible, this is for low-level plumbing,
// where more control over *which* operations should be cached is needed.
func DagOp[T dagql.Typed, A any, R dagql.Typed](
	ctx context.Context,
	srv *dagql.Server,
	self dagql.Instance[T],
	args A,
	fn dagql.NodeFuncHandler[T, A, R],
) (inst R, err error) {
	deps, err := extractLLBDependencies(ctx, self.Self)
	if err != nil {
		return inst, err
	}
	return core.NewRawDagOp[R](ctx, srv, currentIDForDagOp(ctx), deps)
}

type PathFunc[T dagql.Typed] func(ctx context.Context, val dagql.Instance[T]) (string, error)

// DagOpFileWrapper caches a file field as a buildkit operation - this is
// more specialized than DagOpWrapper, since that serializes the value to
// JSON, so we'd just end up with a cached ID instead of the actual content.
func DagOpFileWrapper[T dagql.Typed, A any](
	srv *dagql.Server,
	fn dagql.NodeFuncHandler[T, A, dagql.Instance[*core.File]],
	pfn PathFunc[T],
) dagql.NodeFuncHandler[T, A, dagql.Instance[*core.File]] {
	return func(ctx context.Context, self dagql.Instance[T], args A) (inst dagql.Instance[*core.File], err error) {
		if core.DagOpInContext[core.FSDagOp](ctx) {
			return fn(ctx, self, args)
		}
		return DagOpFile(ctx, srv, self, args, fn, pfn)
	}
}

// DagOpFile creates a FSDagOp from an operation that returns a File
//
// NOTE: prefer DagOpFileWrapper where possible, this is for low-level
// plumbing, where more control over *which* operations should be cached is
// needed.
func DagOpFile[T dagql.Typed, A any](
	ctx context.Context,
	srv *dagql.Server,
	self dagql.Instance[T],
	args A,
	fn dagql.NodeFuncHandler[T, A, dagql.Instance[*core.File]],
	pfn PathFunc[T],
) (inst dagql.Instance[*core.File], _ error) {
	deps, err := extractLLBDependencies(ctx, self.Self)
	if err != nil {
		return inst, err
	}

	filename := "file"
	if pfn != nil {
		// NOTE: if set, the path function must be *somewhat* stable -
		// since it becomes part of the op, then any changes to this
		// invalidate the cache
		filename, err = pfn(ctx, self)
		if err != nil {
			return inst, err
		}
	}

	file, err := core.NewFileDagOp(ctx, srv, currentIDForDagOp(ctx), deps, filename)
	if err != nil {
		return inst, err
	}
	return dagql.NewInstanceForCurrentID(ctx, srv, self, file)
}

// DagOpDirectoryWrapper caches a directory field as a buildkit operation,
// similar to DagOpFileWrapper.
func DagOpDirectoryWrapper[T dagql.Typed, A any](
	srv *dagql.Server,
	fn dagql.NodeFuncHandler[T, A, dagql.Instance[*core.Directory]],
	pfn PathFunc[T],
) dagql.NodeFuncHandler[T, A, dagql.Instance[*core.Directory]] {
	return func(ctx context.Context, self dagql.Instance[T], args A) (inst dagql.Instance[*core.Directory], err error) {
		if core.DagOpInContext[core.FSDagOp](ctx) {
			return fn(ctx, self, args)
		}
		return DagOpDirectory(ctx, srv, self, args, fn, pfn)
	}
}

// NOTE: prefer DagOpDirectoryWrapper where possible, this is for low-level
// plumbing, where more control over *which* operations should be cached is
// needed.
func DagOpDirectory[T dagql.Typed, A any](
	ctx context.Context,
	srv *dagql.Server,
	self dagql.Instance[T],
	args A,
	fn dagql.NodeFuncHandler[T, A, dagql.Instance[*core.Directory]],
	pfn PathFunc[T],
) (inst dagql.Instance[*core.Directory], _ error) {
	deps, err := extractLLBDependencies(ctx, self.Self)
	if err != nil {
		return inst, err
	}

	filename := "/"
	if pfn != nil {
		filename, err = pfn(ctx, self)
		if err != nil {
			return inst, err
		}
	}

	dir, err := core.NewDirectoryDagOp(ctx, srv, currentIDForDagOp(ctx), deps, filename)
	if err != nil {
		return inst, err
	}
	return dagql.NewInstanceForCurrentID(ctx, srv, self, dir)
}

const runDagOpDigestMixin = "runDagOpDigestMixin"

// Return an ID that can be used to force execution of the current ID as a DagOp.
// It works by mixing in a constant value into the current ID's digest.
func currentIDForDagOp(ctx context.Context) *call.ID {
	currentID := dagql.CurrentID(ctx)
	return currentID.WithDigest(dagql.HashFrom(
		currentID.Digest().String(),
		runDagOpDigestMixin,
	))
}

func extractLLBDependencies(ctx context.Context, val any) ([]llb.State, error) {
	hasPBs, ok := dagql.UnwrapAs[core.HasPBDefinitions](val)
	if !ok {
		return nil, nil
	}

	depsDefs, err := hasPBs.PBDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	deps := make([]llb.State, 0, len(depsDefs))
	for _, def := range depsDefs {
		if def == nil || def.Def == nil {
			deps = append(deps, llb.Scratch())
			continue
		}
		op, err := llb.NewDefinitionOp(def)
		if err != nil {
			return nil, err
		}
		deps = append(deps, llb.NewState(op))
	}
	return deps, nil
}
