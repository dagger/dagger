package schema

import (
	"context"
	"fmt"

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
	return func(ctx context.Context, self dagql.ObjectResult[T], args A) (inst R, err error) {
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
	self dagql.ObjectResult[T],
	args A,
	fn dagql.NodeFuncHandler[T, A, R],
) (inst R, err error) {
	deps, err := extractLLBDependencies(ctx, self.Self())
	if err != nil {
		return inst, err
	}
	filename := "output.json"

	fmt.Printf("creating dagop for ID %s\n", currentIDForRawDagOp(ctx, filename).Digest().String())

	return core.NewRawDagOp[R](ctx, srv, &core.RawDagOp{
		ID:       currentIDForRawDagOp(ctx, filename),
		Filename: filename,
	}, deps)
}

type PathFunc[T dagql.Typed, A any] func(ctx context.Context, val T, args A) (string, error)

// DagOpFileWrapper caches a file field as a buildkit operation - this is
// more specialized than DagOpWrapper, since that serializes the value to
// JSON, so we'd just end up with a cached ID instead of the actual content.
func DagOpFileWrapper[T dagql.Typed, A DagOpInternalArgsIface](
	srv *dagql.Server,
	fn dagql.NodeFuncHandler[T, A, dagql.ObjectResult[*core.File]],
	opts ...DagOpOptsFn[T, A],
) dagql.NodeFuncHandler[T, A, dagql.ObjectResult[*core.File]] {
	return func(ctx context.Context, self dagql.ObjectResult[T], args A) (inst dagql.ObjectResult[*core.File], err error) {
		if args.InDagOp() {
			return fn(ctx, self, args)
		}
		file, err := DagOpFile(ctx, srv, self.Self(), args, fn, opts...)
		if err != nil {
			return inst, err
		}
		return dagql.NewObjectResultForCurrentID(ctx, srv, file)
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
	self T,
	args A,
	fn dagql.NodeFuncHandler[T, A, dagql.ObjectResult[*core.File]],
	opts ...DagOpOptsFn[T, A],
) (*core.File, error) {
	o := getOpts(opts...)
	deps, err := extractLLBDependencies(ctx, self)
	if err != nil {
		return nil, err
	}

	filename := "file"
	if o.pfn != nil {
		// NOTE: if set, the path function must be *somewhat* stable -
		// since it becomes part of the op, then any changes to this
		// invalidate the cache
		filename, err = o.pfn(ctx, self, args)
		if err != nil {
			return nil, err
		}
	}

	return core.NewFileDagOp(ctx, srv, &core.FSDagOp{
		ID:   currentIDForFSDagOp(ctx, filename),
		Path: filename,
	}, deps)
}

func DagOpDirectoryWrapperACB[A DagOpInternalArgsIface](
	srv *dagql.Server,
	fn dagql.NodeFuncHandler[*core.Directory, A, dagql.ObjectResult[*core.Directory]],
	opts ...DagOpOptsFn[*core.Directory, A],
) dagql.NodeFuncHandler[*core.Directory, A, dagql.ObjectResult[*core.Directory]] {
	return func(ctx context.Context, self dagql.ObjectResult[*core.Directory], args A) (inst dagql.ObjectResult[*core.Directory], err error) {
		if args.InDagOp() {
			return fn(ctx, self, args)
		}
		dir, err := DagOpDirectoryACB(ctx, srv, self.Self(), args, "", fn, opts...)
		if err != nil {
			return inst, err
		}
		return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
	}
}

// DagOpDirectoryWrapper caches a directory field as a buildkit operation,
// similar to DagOpFileWrapper.
func DagOpDirectoryWrapper[T dagql.Typed, A DagOpInternalArgsIface](
	srv *dagql.Server,
	fn dagql.NodeFuncHandler[T, A, dagql.ObjectResult[*core.Directory]],
	opts ...DagOpOptsFn[T, A],
) dagql.NodeFuncHandler[T, A, dagql.ObjectResult[*core.Directory]] {
	return func(ctx context.Context, self dagql.ObjectResult[T], args A) (inst dagql.ObjectResult[*core.Directory], err error) {
		if args.InDagOp() {
			return fn(ctx, self, args)
		}
		dir, err := DagOpDirectory(ctx, srv, self.Self(), args, "", fn, opts...)
		if err != nil {
			return inst, err
		}
		return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
	}
}

type DagOpOpts[T dagql.Typed, A any] struct {
	pfn PathFunc[T, A]
}

type DagOpOptsFn[T dagql.Typed, A any] func(*DagOpOpts[T, A])

func WithPathFn[T dagql.Typed, A any](pfn PathFunc[T, A]) DagOpOptsFn[T, A] {
	return func(o *DagOpOpts[T, A]) {
		o.pfn = pfn
	}
}

func WithStaticPath[T dagql.Typed, A any](pathVal string) DagOpOptsFn[T, A] {
	return func(o *DagOpOpts[T, A]) {
		o.pfn = func(_ context.Context, _ T, _ A) (string, error) {
			return pathVal, nil
		}
	}
}

func getOpts[T dagql.Typed, A any](opts ...DagOpOptsFn[T, A]) *DagOpOpts[T, A] {
	var o DagOpOpts[T, A]
	for _, optFn := range opts {
		optFn(&o)
	}
	return &o
}

func DagOpDirectoryACB[A any](
	ctx context.Context,
	srv *dagql.Server,
	dir *core.Directory,
	args A,
	data string,
	fn dagql.NodeFuncHandler[*core.Directory, A, dagql.ObjectResult[*core.Directory]],
	opts ...DagOpOptsFn[*core.Directory, A],
) (*core.Directory, error) {
	o := getOpts(opts...)

	argDigest, err := core.DigestOf(args)
	if err != nil {
		return nil, err
	}
	_ = argDigest

	deps, err := extractLLBDependencies(ctx, dir)
	if err != nil {
		return nil, err
	}

	filename := "/"
	if o.pfn != nil {
		filename, err = o.pfn(ctx, dir, args)
		if err != nil {
			return nil, err
		}
	}

	fmt.Printf("ACB ID: %v\n", currentIDForFSDagOp(ctx, filename))

	return core.NewDirectoryDagOpACB(ctx, srv, &core.FSDagOp{
		// FIXME: using this in the cache key means we effectively disable
		// buildkit content caching
		ID:   currentIDForFSDagOp(ctx, filename),
		Path: filename,
		Hack: "yes-hack-this",
	}, deps, argDigest, dir)
}

// NOTE: prefer DagOpDirectoryWrapper where possible, this is for low-level
// plumbing, where more control over *which* operations should be cached is
// needed.
func DagOpDirectory[T dagql.Typed, A any](
	ctx context.Context,
	srv *dagql.Server,
	self T,
	args A,
	data string,
	fn dagql.NodeFuncHandler[T, A, dagql.ObjectResult[*core.Directory]],
	opts ...DagOpOptsFn[T, A],
) (*core.Directory, error) {
	o := getOpts(opts...)

	deps, err := extractLLBDependencies(ctx, self)
	if err != nil {
		return nil, err
	}

	filename := "/"
	if o.pfn != nil {
		filename, err = o.pfn(ctx, self, args)
		if err != nil {
			return nil, err
		}
	}

	fmt.Printf("ACB ID: %v\n", currentIDForFSDagOp(ctx, filename))

	// TODO how to get FS? and any other mounts?
	//mnt := dir.LLB
	//st, err := defToState(mnt.Source)
	//if err != nil {
	//	return err
	//}
	//fmt.Printf("ACB what to do with %+v\n", mnt)

	return core.NewDirectoryDagOp(ctx, srv, &core.FSDagOp{
		// FIXME: using this in the cache key means we effectively disable
		// buildkit content caching
		ID:   currentIDForFSDagOp(ctx, filename),
		Path: filename,
	}, deps)
}

func DagOpContainerWrapper[A DagOpInternalArgsIface](
	srv *dagql.Server,
	fn dagql.NodeFuncHandler[*core.Container, A, dagql.ObjectResult[*core.Container]],
) dagql.NodeFuncHandler[*core.Container, A, dagql.ObjectResult[*core.Container]] {
	return func(ctx context.Context, self dagql.ObjectResult[*core.Container], args A) (inst dagql.ObjectResult[*core.Container], err error) {
		if args.InDagOp() {
			return fn(ctx, self, args)
		}
		ctr, err := DagOpContainer(ctx, srv, self.Self(), args, fn) // this
		if err != nil {
			return inst, err
		}
		return dagql.NewObjectResultForCurrentID(ctx, srv, ctr)
	}
}

func DagOpContainer[A any](
	ctx context.Context,
	srv *dagql.Server,
	ctr *core.Container,
	args A,
	fn dagql.NodeFuncHandler[*core.Container, A, dagql.ObjectResult[*core.Container]],
) (*core.Container, error) {
	argDigest, err := core.DigestOf(args)
	if err != nil {
		return nil, err
	}

	deps, err := extractLLBDependencies(ctx, ctr)
	if err != nil {
		return nil, err
	}
	fmt.Printf("ACB calling NewContainerDagOp\n")
	return core.NewContainerDagOp(ctx, currentIDForContainerDagOp(ctx), argDigest, ctr, deps)
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
	//fmt.Printf("ACB CurrentID is %+v; encoded: %s\n", currentID, currentID.DebugString())

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
)

type FSDagOpInternalArgs struct {
	DagOpInternalArgs

	DagOpPath string `internal:"true" default:"" name:"dagOpPath"`
}

func currentIDForFSDagOp(
	ctx context.Context,
	path string,
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
