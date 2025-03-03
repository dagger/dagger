package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/moby/buildkit/client/llb"
)

// DagOpWrapper caches an arbitrary dagql field as a buildkit operation
func DagOpWrapper[T dagql.Typed, A any, R dagql.Typed](srv *dagql.Server, fn dagql.NodeFuncHandler[T, A, R]) dagql.NodeFuncHandler[T, A, R] {
	return func(ctx context.Context, self dagql.Instance[T], args A) (inst R, err error) {
		if _, ok := core.DagOpFromContext[core.RawDagOp](ctx); ok {
			return fn(ctx, self, args)
		}

		deps, err := extractLLBDependencies(ctx, self.Self)
		if err != nil {
			return inst, err
		}
		return core.NewRawDagOp[R](ctx, srv, dagql.CurrentID(ctx).WithTaint(), deps)
	}
}

// DagOpFileWrapper caches a file field as a buildkit operation - this is
// more specialized than DagOpWrapper, since that serializes the value to
// JSON, so we'd just end up with a cached ID instead of the actual content.
func DagOpFileWrapper[T dagql.Typed, A any](srv *dagql.Server, fn dagql.NodeFuncHandler[T, A, dagql.Instance[*core.File]]) dagql.NodeFuncHandler[T, A, dagql.Instance[*core.File]] {
	return func(ctx context.Context, self dagql.Instance[T], args A) (inst dagql.Instance[*core.File], err error) {
		if _, ok := core.DagOpFromContext[core.DirectoryDagOp](ctx); ok {
			return fn(ctx, self, args)
		}

		deps, err := extractLLBDependencies(ctx, self.Self)
		if err != nil {
			return inst, err
		}

		filename := "file"
		id, err := srv.SelectID(ctx, srv.Root(),
			dagql.Selector{
				Field: "directory",
			},
			dagql.Selector{
				Field: "withFile",
				Args: []dagql.NamedInput{
					{
						Name:  "path",
						Value: dagql.String(filename),
					},
					{
						Name:  "source",
						Value: dagql.NewID[*core.File](dagql.CurrentID(ctx).WithTaint()),
					},
				},
			},
		)
		if err != nil {
			return inst, err
		}

		dir, err := core.NewDirectoryDagOp(ctx, srv, id, deps)
		if err != nil {
			return inst, err
		}
		f, err := dir.File(ctx, filename)
		if err != nil {
			return inst, err
		}
		return dagql.NewInstanceForCurrentID(ctx, srv, self, f)
	}
}

// DagOpDirectoryWrapper caches a directory field as a buildkit operation,
// similar to DagOpFileWrapper.
func DagOpDirectoryWrapper[T dagql.Typed, A any](srv *dagql.Server, fn dagql.NodeFuncHandler[T, A, dagql.Instance[*core.Directory]]) dagql.NodeFuncHandler[T, A, dagql.Instance[*core.Directory]] {
	return func(ctx context.Context, self dagql.Instance[T], args A) (inst dagql.Instance[*core.Directory], err error) {
		if _, ok := core.DagOpFromContext[core.DirectoryDagOp](ctx); ok {
			return fn(ctx, self, args)
		}

		deps, err := extractLLBDependencies(ctx, self.Self)
		if err != nil {
			return inst, err
		}

		id, err := srv.SelectID(ctx, srv.Root(),
			dagql.Selector{
				Field: "directory",
			},
			dagql.Selector{
				Field: "withDirectory",
				Args: []dagql.NamedInput{
					{
						Name:  "path",
						Value: dagql.String(""),
					},
					{
						Name:  "source",
						Value: dagql.NewID[*core.Directory](dagql.CurrentID(ctx).WithTaint()),
					},
				},
			},
		)
		if err != nil {
			return inst, err
		}

		dir, err := core.NewDirectoryDagOp(ctx, srv, id, deps)
		if err != nil {
			return inst, err
		}
		return dagql.NewInstanceForCurrentID(ctx, srv, self, dir)
	}
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
		op, err := llb.NewDefinitionOp(def)
		if err != nil {
			return nil, err
		}
		deps = append(deps, llb.NewState(op))
	}
	return deps, nil
}
