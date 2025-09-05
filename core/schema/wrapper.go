package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/opencontainers/go-digest"
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

	curIDForRawDagOp, err := currentIDForRawDagOp(ctx, filename)
	if err != nil {
		return inst, err
	}
	return core.NewRawDagOp[R](ctx, srv, &core.RawDagOp{
		ID:       curIDForRawDagOp,
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

	curIDForFSDagOp, err := currentIDForFSDagOp(ctx, filename)
	if err != nil {
		return nil, err
	}
	return core.NewFileDagOp(ctx, srv, &core.FSDagOp{
		ID:   curIDForFSDagOp,
		Path: filename,
	}, deps)
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

	FSDagOpInternalArgs
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

func getSelfDigest(a any) (digest.Digest, []llb.State, error) {
	switch x := a.(type) {
	case *core.Directory:
		dgst, err := core.DigestOf(x.WithoutInputs())
		if err != nil {
			return "", nil, err
		}

		var deps []llb.State
		if x.LLB == nil || x.LLB.Def == nil {
			deps = append(deps, llb.Scratch())
		} else {
			op, err := llb.NewDefinitionOp(x.LLB)
			if err != nil {
				return "", nil, err
			}
			deps = append(deps, llb.NewState(op))
		}
		return dgst, deps, err
	case *core.GitRef, *core.Changeset, *core.Query, *core.Host:
		// FIXME: these are weird
		return "", nil, nil // fallback to using dagop ID
	default:
		return "", nil, fmt.Errorf("unable to create digest: unknown type %T", a)
	}
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

	selfDigest, deps, err := getSelfDigest(self)
	if err != nil {
		return nil, err
	}

	argDigest, err := core.DigestOf(args)
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

	curIDForFSDagOp, err := currentIDForFSDagOp(ctx, filename)
	if err != nil {
		return nil, err
	}
	return core.NewDirectoryDagOp(ctx, srv, &core.FSDagOp{
		// FIXME: using this in the cache key means we effectively disable
		// buildkit content caching
		ID:   curIDForFSDagOp,
		Path: filename,
	}, deps, selfDigest, argDigest)
}

func DagOpContainerWrapper[A DagOpInternalArgsIface](
	srv *dagql.Server,
	fn dagql.NodeFuncHandler[*core.Container, A, dagql.ObjectResult[*core.Container]],
) dagql.NodeFuncHandler[*core.Container, A, dagql.ObjectResult[*core.Container]] {
	return func(ctx context.Context, self dagql.ObjectResult[*core.Container], args A) (inst dagql.ObjectResult[*core.Container], err error) {
		if args.InDagOp() {
			return fn(ctx, self, args)
		}
		ctr, err := DagOpContainer(ctx, srv, self.Self(), args, fn)
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

	curIDForContainerDagOp, err := currentIDForContainerDagOp(ctx)
	if err != nil {
		return nil, err
	}
	return core.NewContainerDagOp(ctx, curIDForContainerDagOp, argDigest, ctr)
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
) (*call.ID, error) {
	// we want to honor any custom digest on the currentID, but also need to modify it to
	// indicate this is a dagOp.
	newID := dagql.CurrentID(ctx)
	dgstInputs := []string{newID.Digest().String()}

	args := []*call.Argument{
		call.NewArgument(
			IsDagOpArgName,
			call.NewLiteralBool(true),
			false,
		),
		call.NewArgument(
			RawDagOpFilenameArgName,
			call.NewLiteralString(filename),
			false,
		),
	}

	for _, arg := range args {
		newID = newID.WithArgument(arg)
		argBytes, err := call.AppendArgumentBytes(arg.PB(), nil)
		if err != nil {
			return nil, err
		}
		dgstInputs = append(dgstInputs, string(argBytes))
	}

	dgst := dagql.HashFrom(strings.Join(dgstInputs, "\x00"))

	return newID.WithDigest(dgst), nil
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
) (*call.ID, error) {
	// we want to honor any custom digest on the currentID, but also need to modify it to
	// indicate this is a dagOp.
	newID := dagql.CurrentID(ctx)
	dgstInputs := []string{newID.Digest().String()}

	args := []*call.Argument{
		call.NewArgument(
			IsDagOpArgName,
			call.NewLiteralBool(true),
			false,
		),
		call.NewArgument(
			FSDagOpPathArgName,
			call.NewLiteralString(path),
			false,
		),
	}

	for _, arg := range args {
		newID = newID.WithArgument(arg)
		argBytes, err := call.AppendArgumentBytes(arg.PB(), nil)
		if err != nil {
			return nil, err
		}
		dgstInputs = append(dgstInputs, string(argBytes))
	}

	dgst := dagql.HashFrom(strings.Join(dgstInputs, "\x00"))

	return newID.WithDigest(dgst), nil
}

type ContainerDagOpInternalArgs struct {
	DagOpInternalArgs
}

func currentIDForContainerDagOp(
	ctx context.Context,
) (*call.ID, error) {
	// we want to honor any custom digest on the currentID, but also need to modify it to
	// indicate this is a dagOp.
	newID := dagql.CurrentID(ctx)
	dgstInputs := []string{newID.Digest().String()}

	args := []*call.Argument{
		call.NewArgument(
			IsDagOpArgName,
			call.NewLiteralBool(true),
			false,
		),
	}

	for _, arg := range args {
		newID = newID.WithArgument(arg)
		argBytes, err := call.AppendArgumentBytes(arg.PB(), nil)
		if err != nil {
			return nil, err
		}
		dgstInputs = append(dgstInputs, string(argBytes))
	}

	dgst := dagql.HashFrom(strings.Join(dgstInputs, "\x00"))

	return newID.WithDigest(dgst), nil
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
