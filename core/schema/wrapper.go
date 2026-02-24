package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
)

// DagOpWrapper caches an arbitrary dagql field as a buildkit operation
func DagOpWrapper[T dagql.Typed, A DagOpInternalArgsIface, R dagql.Typed](
	srv *dagql.Server,
	fn dagql.NodeFuncHandler[T, A, R],
) dagql.NodeFuncHandler[T, A, dagql.Result[R]] {
	return func(ctx context.Context, self dagql.ObjectResult[T], args A) (inst dagql.Result[R], err error) {
		if args.InDagOp() {
			val, err := fn(ctx, self, args)
			if err != nil {
				return inst, err
			}
			return dagql.NewResultForCurrentID(ctx, val)
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
) (inst dagql.Result[R], err error) {
	deps, err := core.InputsOf(ctx, args)
	if err != nil {
		return inst, err
	}

	filename := rawDagOpFilename
	curIDForRawDagOp, err := currentIDForRawDagOp(ctx, filename)
	if err != nil {
		return inst, err
	}

	resultID := dagql.CurrentID(ctx)
	if resultID != nil {
		resultID = resultID.AppendEffectIDs(curIDForRawDagOp.OutputEquivalentDigest().String())
	}

	val, err := core.NewRawDagOp[R](ctx, srv, &core.RawDagOp{
		ID:       curIDForRawDagOp, // FIXME: using this in the cache key means we effectively disable buildkit content caching
		Filename: filename,
	}, deps)
	if err != nil {
		return inst, err
	}
	return dagql.NewResultForID(val, resultID)
}

const rawDagOpFilename = "output.json"

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
		file, effectID, err := DagOpFile(ctx, srv, self.Self(), args, fn, opts...)
		if err != nil {
			return inst, err
		}
		resultID := dagql.CurrentID(ctx)
		if effectID != "" && resultID != nil {
			resultID = resultID.AppendEffectIDs(effectID)
		}
		inst, err = dagql.NewObjectResultForID(file, srv, resultID)
		if err != nil {
			return inst, err
		}
		return inst, nil
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
) (*core.File, string, error) {
	o := getOpts(opts...)

	filename := "file"
	if o.pfn != nil {
		// NOTE: if set, the path function must be *somewhat* stable -
		// since it becomes part of the op, then any changes to this
		// invalidate the cache
		var err error
		filename, err = o.pfn(ctx, self, args)
		if err != nil {
			return nil, "", err
		}
	}

	selfDigest, deps, err := getSelfDigest(ctx, self)
	if err != nil {
		return nil, "", err
	}
	argDigest, err := core.DigestOf(args)
	if err != nil {
		return nil, "", err
	}

	curIDForFSDagOp, err := currentIDForFSDagOp(ctx, filename)
	if err != nil {
		return nil, "", err
	}

	cacheKey := digest.FromString(
		strings.Join([]string{
			selfDigest.String(),
			argDigest.String(),
		}, "\x00"),
	)

	f, err := core.NewFileDagOp(ctx, srv, &core.FSDagOp{
		ID:       curIDForFSDagOp,
		Path:     filename,
		CacheKey: cacheKey,
	}, deps)
	if err != nil {
		return nil, "", err
	}
	return f, curIDForFSDagOp.OutputEquivalentDigest().String(), nil
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

		dir, effectID, err := DagOpDirectory(ctx, srv, self.Self(), args, "", fn, opts...)
		if err != nil {
			return inst, err
		}

		resultID := dagql.CurrentID(ctx)
		if effectID != "" && resultID != nil {
			resultID = resultID.AppendEffectIDs(effectID)
		}
		dirResult, err := dagql.NewObjectResultForID(dir, srv, resultID)
		if err != nil {
			return inst, err
		}

		o := getOpts(opts...)
		if !o.hashContentDir {
			return dirResult, nil
		}

		query, err := core.CurrentQuery(ctx)
		if err != nil {
			return inst, fmt.Errorf("failed to get current query: %w", err)
		}

		bk, err := query.Buildkit(ctx)
		if err != nil {
			return inst, fmt.Errorf("failed to get buildkit client: %w", err)
		}

		return core.MakeDirectoryContentHashed(ctx, bk, dirResult)
	}
}

type DagOpOpts[T dagql.Typed, A any] struct {
	pfn            PathFunc[T, A]
	hashContentDir bool
	keepImageRef   bool

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

func WithHashContentDir[T dagql.Typed, A any]() DagOpOptsFn[T, A] {
	return func(o *DagOpOpts[T, A]) {
		o.hashContentDir = true
	}
}

func KeepImageRef[T dagql.Typed, A any](keep bool) DagOpOptsFn[T, A] {
	return func(o *DagOpOpts[T, A]) {
		o.keepImageRef = keep
	}
}

func getOpts[T dagql.Typed, A any](opts ...DagOpOptsFn[T, A]) *DagOpOpts[T, A] {
	var o DagOpOpts[T, A]
	for _, optFn := range opts {
		optFn(&o)
	}
	return &o
}

func getSelfDigest(ctx context.Context, a any) (digest.Digest, []llb.State, error) {
	switch x := a.(type) {
	case *core.Container:
		dgst, err := core.DigestOf(x.WithoutInputs())
		if err != nil {
			return "", nil, err
		}

		var deps []llb.State
		var fsLLB *pb.Definition
		if x.FS != nil && x.FS.Self() != nil {
			fsLLB = x.FS.Self().LLB
		}
		if fsLLB == nil || fsLLB.Def == nil {
			deps = append(deps, llb.Scratch())
		} else {
			op, err := llb.NewDefinitionOp(fsLLB)
			if err != nil {
				return "", nil, err
			}
			deps = append(deps, llb.NewState(op))
		}

		for _, m := range x.Mounts {
			mLLB := m.GetLLB()
			if mLLB != nil && mLLB.Def != nil {
				op, err := llb.NewDefinitionOp(mLLB)
				if err != nil {
					return "", nil, err
				}
				deps = append(deps, llb.NewState(op))
			}
		}

		return dgst, deps, err
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
	case *core.File:
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
	case
		// FIXME: these are weird
		*core.Changeset,
		*core.GitRef,
		*core.GitRepository,
		*core.Host,
		*core.Query,
		*core.Workspace:
		// fallback to using dagop ID
		return dagql.CurrentID(ctx).OutputEquivalentDigest(), nil, nil
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
) (*core.Directory, string, error) {
	o := getOpts(opts...)

	selfDigest, deps, err := getSelfDigest(ctx, self)
	if err != nil {
		return nil, "", err
	}
	argDigest, err := core.DigestOf(args)
	if err != nil {
		return nil, "", err
	}
	argDeps, err := core.InputsOf(ctx, args)
	if err != nil {
		return nil, "", err
	}
	deps = append(deps, argDeps...)

	filename := "/"
	if o.pfn != nil {
		filename, err = o.pfn(ctx, self, args)
		if err != nil {
			return nil, "", err
		}
	}

	cacheKey := digest.FromString(
		strings.Join([]string{
			selfDigest.String(),
			argDigest.String(),
		}, "\x00"),
	)

	curIDForFSDagOp, err := currentIDForFSDagOp(ctx, filename)
	if err != nil {
		return nil, "", err
	}

	dir, err := core.NewDirectoryDagOp(ctx, srv, &core.FSDagOp{
		ID:       curIDForFSDagOp,
		Path:     filename,
		CacheKey: cacheKey,
	}, deps, selfDigest, argDigest)
	if err != nil {
		return nil, "", err
	}
	return dir, curIDForFSDagOp.OutputEquivalentDigest().String(), nil
}

func DagOpContainerWrapper[A DagOpInternalArgsIface](
	srv *dagql.Server,
	fn dagql.NodeFuncHandler[*core.Container, A, dagql.ObjectResult[*core.Container]],
	opts ...DagOpOptsFn[*core.Container, A],
) dagql.NodeFuncHandler[*core.Container, A, dagql.ObjectResult[*core.Container]] {
	o := getOpts(opts...)
	return func(ctx context.Context, self dagql.ObjectResult[*core.Container], args A) (inst dagql.ObjectResult[*core.Container], err error) {
		if args.InDagOp() {
			return fn(ctx, self, args)
		}
		ctr, effectID, err := DagOpContainer(ctx, srv, self.Self(), args, nil)
		if err != nil {
			return inst, err
		}
		if !o.keepImageRef {
			ctr.ImageRef = ""
		}
		resultID := dagql.CurrentID(ctx)
		if effectID != "" && resultID != nil {
			resultID = resultID.AppendEffectIDs(effectID)
		}
		inst, err = dagql.NewObjectResultForID(ctr, srv, resultID)
		if err != nil {
			return inst, err
		}
		return inst, nil
	}
}

func DagOpContainer[A any](
	ctx context.Context,
	srv *dagql.Server,
	ctr *core.Container,
	args A,
	execMD *buildkit.ExecutionMetadata,
) (*core.Container, string, error) {
	deps, err := core.InputsOf(ctx, args)
	if err != nil {
		return nil, "", err
	}

	curIDForContainerDagOp, err := currentIDForContainerDagOp(ctx)
	if err != nil {
		return nil, "", err
	}

	ctrRes, err := core.NewContainerDagOp(ctx, curIDForContainerDagOp, deps, ctr, execMD)
	if err != nil {
		return nil, "", err
	}
	return ctrRes, curIDForContainerDagOp.OutputEquivalentDigest().String(), nil
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
	id := dagql.CurrentID(ctx)
	if id == nil {
		return nil, fmt.Errorf("current ID is nil")
	}
	id = id.WithArgument(call.NewArgument(
		IsDagOpArgName,
		call.NewLiteralBool(true),
		false,
	))
	id = id.WithArgument(call.NewArgument(
		RawDagOpFilenameArgName,
		call.NewLiteralString(filename),
		false,
	))
	return id, nil
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
	id := dagql.CurrentID(ctx)
	if id == nil {
		return nil, fmt.Errorf("current ID is nil")
	}
	id = id.WithArgument(call.NewArgument(
		IsDagOpArgName,
		call.NewLiteralBool(true),
		false,
	))
	id = id.WithArgument(call.NewArgument(
		FSDagOpPathArgName,
		call.NewLiteralString(path),
		false,
	))
	return id, nil
}

type ContainerDagOpInternalArgs struct {
	DagOpInternalArgs
}

func currentIDForContainerDagOp(
	ctx context.Context,
) (*call.ID, error) {
	id := dagql.CurrentID(ctx)
	if id == nil {
		return nil, fmt.Errorf("current ID is nil")
	}
	id = id.WithArgument(call.NewArgument(
		IsDagOpArgName,
		call.NewLiteralBool(true),
		false,
	))
	return id, nil
}

// DagOpChangesetWrapper caches a changeset field as a buildkit operation.
// After JSON deserialization, it resolves the ObjectResult references that
// couldn't be directly unmarshaled.
func DagOpChangesetWrapper[T dagql.Typed, A DagOpInternalArgsIface](
	srv *dagql.Server,
	fn dagql.NodeFuncHandler[T, A, *core.Changeset],
) dagql.NodeFuncHandler[T, A, dagql.Result[*core.Changeset]] {
	return func(ctx context.Context, self dagql.ObjectResult[T], args A) (inst dagql.Result[*core.Changeset], err error) {
		if args.InDagOp() {
			val, err := fn(ctx, self, args)
			if err != nil {
				return inst, err
			}
			return dagql.NewResultForCurrentID(ctx, val)
		}
		cs, err := DagOp(ctx, srv, self, args, fn)
		if err != nil {
			return inst, err
		}
		// Resolve refs after JSON deserialization to reconstruct ObjectResult fields
		if err := cs.Self().ResolveRefs(ctx, srv); err != nil {
			return inst, fmt.Errorf("resolve changeset refs: %w", err)
		}
		return cs, nil
	}
}
