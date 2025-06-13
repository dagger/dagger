package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/moby/buildkit/client/llb"
)

// DagOpWrapper caches an arbitrary dagql field as a buildkit operation
func DagOpWrapper[T dagql.Typed, A DagOpInternalArgsIface, R dagql.Typed](
	srv *dagql.Server,
	fn dagql.NodeFuncHandler[T, A, R],
) dagql.NodeFuncHandler[T, A, R] {
	return func(ctx context.Context, self dagql.Instance[T], args A) (inst R, err error) {
		if args.InDagOp() {
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
	filename := "output.json"
	return core.NewRawDagOp[R](ctx, srv, &core.RawDagOp{
		ID:       currentIDForRawDagOp(ctx, filename),
		Filename: filename,
	}, deps)
}

type PathFunc[T dagql.Typed, A any] func(ctx context.Context, val dagql.Instance[T], args A) (string, error)

// DagOpFileWrapper caches a file field as a buildkit operation - this is
// more specialized than DagOpWrapper, since that serializes the value to
// JSON, so we'd just end up with a cached ID instead of the actual content.
func DagOpFileWrapper[T dagql.Typed, A DagOpInternalArgsIface](
	srv *dagql.Server,
	fn dagql.NodeFuncHandler[T, A, dagql.Instance[*core.File]],
	pfn PathFunc[T, A],
) dagql.NodeFuncHandler[T, A, dagql.Instance[*core.File]] {
	return func(ctx context.Context, self dagql.Instance[T], args A) (inst dagql.Instance[*core.File], err error) {
		if args.InDagOp() {
			return fn(ctx, self, args)
		}
		return DagOpFile(ctx, srv, self, args, "", fn, pfn)
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
	data string,
	fn dagql.NodeFuncHandler[T, A, dagql.Instance[*core.File]],
	pfn PathFunc[T, A],
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
		filename, err = pfn(ctx, self, args)
		if err != nil {
			return inst, err
		}
	}

	file, err := core.NewFileDagOp(ctx, srv, &core.FSDagOp{
		ID:   currentIDForFSDagOp(ctx, filename, data),
		Path: filename,
		Data: data,
	}, deps)
	if err != nil {
		return inst, err
	}

	return dagql.NewInstanceForCurrentID(ctx, srv, self, file)
}

// DagOpDirectoryWrapper caches a directory field as a buildkit operation,
// similar to DagOpFileWrapper.
func DagOpDirectoryWrapper[T dagql.Typed, A DagOpInternalArgsIface](
	srv *dagql.Server,
	fn dagql.NodeFuncHandler[T, A, dagql.Instance[*core.Directory]],
	pfn PathFunc[T, A],
) dagql.NodeFuncHandler[T, A, dagql.Instance[*core.Directory]] {
	return func(ctx context.Context, self dagql.Instance[T], args A) (inst dagql.Instance[*core.Directory], err error) {
		if args.InDagOp() {
			return fn(ctx, self, args)
		}
		return DagOpDirectory(ctx, srv, self, args, "", fn, pfn)
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
	data string,
	fn dagql.NodeFuncHandler[T, A, dagql.Instance[*core.Directory]],
	pfn PathFunc[T, A],
) (inst dagql.Instance[*core.Directory], _ error) {
	deps, err := extractLLBDependencies(ctx, self.Self)
	if err != nil {
		return inst, err
	}

	filename := "/"
	if pfn != nil {
		filename, err = pfn(ctx, self, args)
		if err != nil {
			return inst, err
		}
	}

	dir, err := core.NewDirectoryDagOp(ctx, srv, &core.FSDagOp{
		// FIXME: using this in the cache key means we effectively disable
		// buildkit content caching
		ID:   currentIDForFSDagOp(ctx, filename, data),
		Path: filename,
		Data: data,
	}, deps)
	if err != nil {
		return inst, err
	}
	return dagql.NewInstanceForCurrentID(ctx, srv, self, dir)
}

func DagOpContainerWrapper[A DagOpInternalArgsIface](
	srv *dagql.Server,
	fn dagql.NodeFuncHandler[*core.Container, A, dagql.Instance[*core.Container]],
) dagql.NodeFuncHandler[*core.Container, A, dagql.Instance[*core.Container]] {
	return func(ctx context.Context, self dagql.Instance[*core.Container], args A) (inst dagql.Instance[*core.Container], err error) {
		if args.InDagOp() {
			return fn(ctx, self, args)
		}
		return DagOpContainer(ctx, srv, self, args, nil, fn)
	}
}

func DagOpContainer[A any](
	ctx context.Context,
	srv *dagql.Server,
	self dagql.Instance[*core.Container],
	args A,
	data any,
	fn dagql.NodeFuncHandler[*core.Container, A, dagql.Instance[*core.Container]],
) (inst dagql.Instance[*core.Container], _ error) {
	deps, err := extractLLBDependencies(ctx, self.Self)
	if err != nil {
		return inst, err
	}

	ctr, err := core.NewContainerDagOp(ctx, currentIDForContainerDagOp(ctx), self.Self, deps)
	if err != nil {
		return inst, err
	}
	return dagql.NewInstanceForCurrentID(ctx, srv, self, ctr)
}

const (
	// defined in core package to support telemetry code accessing it too
	IsDagOpArgName = core.IsDagOpArgName
)

type DagOpInternalArgsIface interface {
	InDagOp() bool
}

type DagOpInternalArgs struct {
	IsDagOp bool `internal:"true" default:"false" name:"isDagOp"`
}

func (d DagOpInternalArgs) InDagOp() bool {
	return d.IsDagOp
}

const (
	RawDagOpFilenameArgName = "dagOpFilename"
)

type RawDagOpInternalArgs struct {
	DagOpInternalArgs

	DagOpFilename string `internal:"true" default:"" name:"dagOpFilename"`
}

func currentIDForRawDagOp(
	ctx context.Context,
	filename string,
) *call.ID {
	currentID := dagql.CurrentID(ctx)

	return currentID.
		WithArgument(call.NewArgument(
			IsDagOpArgName,
			call.NewLiteralBool(true),
			false,
		)).
		WithArgument(call.NewArgument(
			RawDagOpFilenameArgName,
			call.NewLiteralString(filename),
			false,
		))
}

const (
	FSDagOpPathArgName = "dagOpPath"
	FSDagOpDataArgName = "dagOpData"
)

type FSDagOpInternalArgs struct {
	DagOpInternalArgs

	DagOpPath string `internal:"true" default:"" name:"dagOpPath"`
	DagOpData string `internal:"true" default:"" name:"dagOpData"`
}

func currentIDForFSDagOp(
	ctx context.Context,
	path string,
	data string,
) *call.ID {
	currentID := dagql.CurrentID(ctx)

	return currentID.
		WithArgument(call.NewArgument(
			IsDagOpArgName,
			call.NewLiteralBool(true),
			false,
		)).
		WithArgument(call.NewArgument(
			FSDagOpPathArgName,
			call.NewLiteralString(path),
			false,
		)).
		WithArgument(call.NewArgument(
			FSDagOpDataArgName,
			call.NewLiteralString(data),
			false,
		))
}

const (
	ContainerDagOpOutputCountArgName = "dagOpOutputCount"
)

type ContainerDagOpInternalArgs struct {
	DagOpInternalArgs
}

func currentIDForContainerDagOp(
	ctx context.Context,
) *call.ID {
	currentID := dagql.CurrentID(ctx)

	return currentID.
		WithArgument(call.NewArgument(
			IsDagOpArgName,
			call.NewLiteralBool(true),
			false,
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
