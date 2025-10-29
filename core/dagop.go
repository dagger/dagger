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
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	"github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	bkgw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/snapshot"
	"github.com/dagger/dagger/internal/buildkit/solver"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/worker"
	"github.com/opencontainers/go-digest"
)

const (
	keyDaggerDigest = "dagger.digest"
	daggerDigestIdx = keyDaggerDigest + ":"
)

func init() {
	buildkit.RegisterCustomOp(FSDagOp{})
	buildkit.RegisterCustomOp(RawDagOp{})
	buildkit.RegisterCustomOp(ContainerDagOp{})
}

// NewDirectoryDagOp takes a target ID for a Directory, and returns a Directory
// for it, computing the actual dagql query inside a buildkit operation, which
// allows for efficiently caching the result.
func NewDirectoryDagOp(
	ctx context.Context,
	srv *dagql.Server,
	dagop *FSDagOp,
	inputs []llb.State,
	selfDigest digest.Digest,
	argDigest digest.Digest,
) (*Directory, error) {
	if selfDigest == "" || argDigest == "" {
		// fall back to using op ID (which will return a different CacheMap value for each op
		dagop.CacheKey = digest.FromString(
			strings.Join([]string{
				dagop.ID.Digest().String(),
				dagop.Path,
			}, "\x00"))
	} else {
		dagop.CacheKey = digest.FromString(
			strings.Join([]string{
				selfDigest.String(),
				argDigest.String(),
			}, "\x00"),
		)
	}

	st, err := newFSDagOp[*Directory](ctx, dagop, inputs)
	if err != nil {
		return nil, err
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current query: %w", err)
	}
	return NewDirectorySt(ctx, st, dagop.Path, query.Platform(), nil)
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
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current query: %w", err)
	}
	return NewFileSt(ctx, st, dagop.Path, query.Platform(), nil)
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

	CacheKey digest.Digest
}

func (op FSDagOp) Name() string {
	return "dagop.fs"
}

func (op FSDagOp) Backend() buildkit.CustomOpBackend {
	return &op
}

func (op FSDagOp) Digest() (digest.Digest, error) {
	return digest.FromString(strings.Join([]string{
		engine.BaseVersion(engine.Version),
		op.ID.Digest().String(),
		op.Path,
	}, "\x00")), nil
}

func (op FSDagOp) CacheMap(ctx context.Context, cm *solver.CacheMap) (*solver.CacheMap, error) {
	var inputs []string
	if op.CacheKey.String() == "" {
		// TODO replace this with a panic("this shouldnt happen") once all FSDagOps are correctly created
		inputs = []string{
			engine.BaseVersion(engine.Version),
			op.ID.Digest().String(),
			op.Path,
		}
	} else {
		inputs = []string{
			engine.BaseVersion(engine.Version),
			op.CacheKey.String(),
		}
	}
	cm.Digest = digest.FromString(strings.Join(inputs, "\x00"))

	// Read digests of results from dagger ref metadata, only doing a real content hash if that's
	// not available. Reading the digest from the ref metadata enables us to use possibly optimized
	// digests as determined from within dag-ops. e.g. `Directory.Without` may determine that no
	// files were removed and thus we can reuse the parent object digest.
	for i, dep := range cm.Deps {
		dep.PreprocessFunc = nil
		origFunc := dep.ComputeDigestFunc
		dep.ComputeDigestFunc = func(ctx context.Context, res solver.Result, g bksession.Group) (digest.Digest, error) {
			workerRef, ok := res.Sys().(*worker.WorkerRef)
			if !ok {
				return "", fmt.Errorf("invalid ref: %T", res.Sys())
			}
			ref := workerRef.ImmutableRef
			if ref == nil {
				return origFunc(ctx, res, g)
			}

			dgstStr := ref.GetString(keyDaggerDigest)
			if dgstStr == "" {
				// fall back to original function if no dagger digest found
				return origFunc(ctx, res, g)
			}

			return digest.Digest(dgstStr), nil
		}
		cm.Deps[i] = dep
	}

	return cm, nil
}

func (op FSDagOp) Exec(ctx context.Context, g bksession.Group, inputs []solver.Result, opt buildkit.OpOpts) (outputs []solver.Result, err error) {
	query, ok := opt.Server.Root().Unwrap().(*Query)
	if !ok {
		return nil, fmt.Errorf("server root was %T", opt.Server.Root())
	}
	ctx = ContextWithQuery(ctx, query)
	obj, err := opt.Server.LoadType(ctx, op.ID)
	if err != nil {
		return nil, err
	}

	var solverRes solver.Result
	switch inst := obj.Unwrap().(type) {
	case *Directory:
		if inst.Result != nil {
			solverRes = worker.NewWorkerRefResult(inst.Result.Clone(), opt.Worker)
			break
		}

		res, err := inst.Evaluate(ctx)
		if err != nil {
			return nil, err
		}
		ref, err := res.Ref.Result(ctx)
		if err != nil {
			return nil, err
		}
		solverRes = ref

	case *File:
		if inst.Result != nil {
			solverRes = worker.NewWorkerRefResult(inst.Result.Clone(), opt.Worker)
			break
		}

		res, err := inst.Evaluate(ctx)
		if err != nil {
			return nil, err
		}
		ref, err := res.Ref.Result(ctx)
		if err != nil {
			return nil, err
		}
		solverRes = ref

	default:
		// shouldn't happen, should have errored in DagLLB already
		return nil, fmt.Errorf("expected FS to be selected, instead got %T", obj)
	}

	if solverRes == nil {
		solverRes = worker.NewWorkerRefResult(nil, opt.Worker)
	}

	workerRef, ok := solverRes.Sys().(*worker.WorkerRef)
	if !ok {
		return nil, fmt.Errorf("invalid ref: %T", solverRes.Sys())
	}
	ref := workerRef.ImmutableRef
	if ref != nil {
		idDgst := obj.ID().Digest().String()
		if err := ref.SetString(keyDaggerDigest, idDgst, daggerDigestIdx+idDgst); err != nil {
			return nil, fmt.Errorf("failed to set dagger digest on ref: %w", err)
		}
	}

	return []solver.Result{solverRes}, nil
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

	f, err := NewFileSt(ctx, st, dagop.Filename, Platform{}, nil)
	if err != nil {
		return t, err
	}
	dt, err := f.Contents(ctx, nil, nil)
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
		engine.BaseVersion(engine.Version),
		op.ID.Digest().String(),
		op.Filename,
	}, "\x00")), nil
}

func (op RawDagOp) CacheMap(ctx context.Context, cm *solver.CacheMap) (*solver.CacheMap, error) {
	cm.Digest = digest.FromString(strings.Join([]string{
		engine.BaseVersion(engine.Version),
		op.ID.Digest().String(),
		op.Filename,
	}, "\x00"))

	// disable content hashing of inputs, which is extremely expensive; we rely
	// on the content digests of dagql inputs being mixed into the op ID digest
	// instead now
	for i, dep := range cm.Deps {
		dep.PreprocessFunc = nil
		dep.ComputeDigestFunc = nil
		cm.Deps[i] = dep
	}

	return cm, nil
}

func (op RawDagOp) Exec(ctx context.Context, g bksession.Group, inputs []solver.Result, opt buildkit.OpOpts) (outputs []solver.Result, retErr error) {
	query, ok := opt.Server.Root().Unwrap().(*Query)
	if !ok {
		return nil, fmt.Errorf("server root was %T", opt.Server.Root())
	}
	result, err := opt.Server.LoadType(ContextWithQuery(ctx, query), op.ID)
	if err != nil {
		return nil, err
	}

	ref, err := query.BuildkitCache().New(ctx, nil, g,
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
	err = enc.Encode(result.Unwrap())
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

func NewContainerDagOp(
	ctx context.Context,
	id *call.ID,
	argDigest digest.Digest,
	inputs []llb.State,
	ctr *Container,
) (*Container, error) {
	mounts, ctrInputs, dgsts, _, outputCount, err := getAllContainerMounts(ctx, ctr)
	if err != nil {
		return nil, err
	}
	inputs = append(inputs, ctrInputs...)

	dagop := &ContainerDagOp{
		ID: id,
		CacheKey: digest.FromString(
			strings.Join([]string{
				id.Digest().String(),
				engine.BaseVersion(engine.Version),
			}, "\x00"),
		),
		ContainerMountData: ContainerMountData{
			Mounts:      mounts,
			Digests:     dgsts,
			OutputCount: outputCount,
		},
	}

	st, err := newContainerDagOp(ctx, dagop, inputs)
	if err != nil {
		return nil, err
	}

	sts := make([]llb.State, dagop.OutputCount)
	for _, mount := range mounts {
		if mount.Output == pb.SkipOutput {
			continue
		}
		out := buildkit.StateIdx(ctx, st, mount.Output, nil)
		sts[mount.Output] = out
	}

	ctr = ctr.Clone()
	err = dagop.setAllContainerMounts(ctx, ctr, sts)
	if err != nil {
		return nil, err
	}

	return ctr, nil
}

func newContainerDagOp(
	ctx context.Context,
	dagop *ContainerDagOp,
	inputs []llb.State,
) (llb.State, error) {
	if dagop.ID == nil {
		return llb.State{}, fmt.Errorf("dagop ID is nil")
	}

	var t Container
	requiredType := t.Type().NamedType
	if dagop.ID.Type().NamedType() != requiredType {
		return llb.State{}, fmt.Errorf("expected %s to be selected, instead got %s", requiredType, dagop.ID.Type().NamedType())
	}

	return newDagOpLLB(ctx, dagop, dagop.ID, inputs)
}

type ContainerDagOp struct {
	ID       *call.ID
	CacheKey digest.Digest

	ContainerMountData
}

type ContainerMountData struct {
	// inputs are all the inputs provided to the op
	//
	// be careful accessing it directly (stable order is not guaranteed, and it
	// may also contain a bunch of other stuff), ideally access it through a
	// known pb.Mount.Output index.
	Inputs []solver.Result

	// all the container mounts - the order here should be guaranteed:
	// - rootfs is at 0
	// - meta mount is at 1
	// - nth container.Mounts is at n+2
	// - secret/socket mounts are at the very end
	Mounts []*pb.Mount

	// The digests corresponding to each Mount, or "" if no digest available.
	Digests []digest.Digest

	// the number of outputs produced
	OutputCount int
}

func (mounts ContainerMountData) InputRefs() []bkcache.ImmutableRef {
	refs := make([]bkcache.ImmutableRef, 0, len(mounts.Inputs))
	for _, input := range mounts.Inputs {
		refs = append(refs, input.Sys().(*worker.WorkerRef).ImmutableRef)
	}
	return refs
}

type mountDataContextKey struct{}

func ctxWithMountData(ctx context.Context, mount ContainerMountData) context.Context {
	return context.WithValue(ctx, mountDataContextKey{}, mount)
}

func CurrentMountData(ctx context.Context) (ContainerMountData, bool) {
	opt, ok := ctx.Value(mountDataContextKey{}).(ContainerMountData)
	return opt, ok
}

func (op ContainerDagOp) Name() string {
	return "dagop.ctr"
}

func (op ContainerDagOp) Backend() buildkit.CustomOpBackend {
	return &op
}

func (op ContainerDagOp) Digest() (digest.Digest, error) {
	return op.CacheKey, nil
}

func (op ContainerDagOp) CacheMap(ctx context.Context, cm *solver.CacheMap) (*solver.CacheMap, error) {
	// Use our precomputed cache key and additional content-hashing for mounts that are associated with
	// a known digest.
	cm.Digest = op.CacheKey
	for i, mount := range op.Mounts {
		if mount.Input == pb.Empty {
			// No inputs, so no content-caching to apply.
			continue
		}
		cm.Deps[mount.Input].PreprocessFunc = nil

		if dgst := op.Digests[i]; dgst != "" {
			cm.Deps[mount.Input].ComputeDigestFunc = func(
				context.Context,
				solver.Result,
				bksession.Group,
			) (digest.Digest, error) {
				return dgst, nil
			}
		} else {
			cm.Deps[mount.Input].ComputeDigestFunc = nil
		}
	}
	return cm, nil
}

func (op ContainerDagOp) Exec(ctx context.Context, g bksession.Group, inputs []solver.Result, opt buildkit.OpOpts) (outputs []solver.Result, retErr error) {
	loadCtx := ctx

	query, ok := opt.Server.Root().Unwrap().(*Query)
	if !ok {
		return nil, fmt.Errorf("server root was %T", opt.Server.Root())
	}
	loadCtx = ContextWithQuery(loadCtx, query)

	mountData := op.ContainerMountData
	mountData.Inputs = inputs
	loadCtx = ctxWithMountData(loadCtx, mountData)

	obj, err := opt.Server.LoadType(loadCtx, op.ID)
	if err != nil {
		return nil, err
	}

	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	switch inst := obj.Unwrap().(type) {
	case *Container:
		return extractContainerBkOutputs(ctx, inst, bk, opt.Worker, op.ContainerMountData)
	default:
		// shouldn't happen, should have errored in DagLLB already
		return nil, fmt.Errorf("expected FS to be selected, instead got %T", obj)
	}
}

// getAllContainerMounts gets the list of all mounts for a container, as well as all the
// inputs that are part of it, and the total number of outputs. Each mount's
// Input maps to an index in the returned states.
func getAllContainerMounts(ctx context.Context, container *Container) (
	mounts []*pb.Mount,
	states []llb.State,
	dgsts []digest.Digest,
	refs []bkcache.ImmutableRef,
	outputCount int,
	_ error,
) {
	outputIdx := 0
	inputIdxs := map[string]pb.InputIndex{}

	// addMount converts a ContainerMount and creates a corresponding buildkit
	// mount, creating an input if required
	addMount := func(mnt ContainerMount) error {
		mount := &pb.Mount{
			Dest:         mnt.Target,
			Input:        pb.Empty,
			Output:       pb.OutputIndex(outputIdx),
			ContentCache: pb.MountContentCache_DEFAULT,
		}
		if mnt.Readonly {
			mount.Readonly = true
			mount.Output = pb.SkipOutput
		}

		var llb *pb.Definition
		var res bkcache.ImmutableRef
		var dgst digest.Digest
		handleMount(mnt,
			func(dirMnt *dagql.ObjectResult[*Directory]) {
				mount.Selector = dirMnt.Self().Dir
				llb = dirMnt.Self().LLB
				res = dirMnt.Self().Result
				dgst = dirMnt.ID().Digest()
			},
			func(fileMnt *dagql.ObjectResult[*File]) {
				mount.Selector = fileMnt.Self().File
				llb = fileMnt.Self().LLB
				res = fileMnt.Self().Result
				dgst = fileMnt.ID().Digest()
			},
			func(cache *CacheMountSource) {
				mount.Selector = cache.BasePath
				llb = cache.Base
				mount.Output = pb.SkipOutput
				mount.MountType = pb.MountType_CACHE
				mount.CacheOpt = &pb.CacheOpt{
					ID: cache.ID,
				}
				switch cache.SharingMode {
				case CacheSharingModeShared:
					mount.CacheOpt.Sharing = pb.CacheSharingOpt_SHARED
				case CacheSharingModePrivate:
					mount.CacheOpt.Sharing = pb.CacheSharingOpt_PRIVATE
				case CacheSharingModeLocked:
					mount.CacheOpt.Sharing = pb.CacheSharingOpt_LOCKED
				}
			},
			func(tmpfs *TmpfsMountSource) {
				mount.Output = pb.SkipOutput
				mount.MountType = pb.MountType_TMPFS
				mount.TmpfsOpt = &pb.TmpfsOpt{
					Size_: int64(tmpfs.Size),
				}
			},
		)

		st, err := defToState(llb)
		if err != nil {
			return err
		}

		// track and cache this input index, since duplicates are unnecessary
		// also buildkit's FileOp (which is underlying our DagOp) will
		// remove them if we don't, which results in significant confusion
		switch {
		case res != nil:
			indexKey := res.ID()
			if idx, ok := inputIdxs[indexKey]; ok {
				// we already track this input, reuse the index
				mount.Input = idx
			} else {
				mount.Input = pb.InputIndex(len(states))
				inputIdxs[indexKey] = mount.Input
				states = append(states, st)
				refs = append(refs, res)
			}
		case st.Output() != nil:
			dag, err := buildkit.DefToDAG(llb)
			if err != nil {
				return err
			}
			indexKey := dag.OpDigest.String()
			if idx, ok := inputIdxs[indexKey]; ok {
				// we already track this input, reuse the index
				mount.Input = idx
			} else {
				mount.Input = pb.InputIndex(len(states))
				inputIdxs[indexKey] = mount.Input
				states = append(states, st)
				refs = append(refs, res)
			}
		}

		mounts = append(mounts, mount)
		dgsts = append(dgsts, dgst)
		if mount.Output != pb.SkipOutput {
			outputIdx++
		}
		return nil
	}

	// root mount
	if err := addMount(ContainerMount{
		Target:          "/",
		DirectorySource: container.FS,
	}); err != nil {
		return nil, nil, nil, nil, 0, err
	}

	// meta mount
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, nil, nil, nil, 0, fmt.Errorf("failed to get current dagql server: %w", err)
	}
	metaDir := &Directory{
		Dir:      "/",
		Platform: container.Platform,
		Services: container.Services,
	}
	if container.Meta != nil {
		metaDir.LLB = container.Meta.LLB
		metaDir.Result = container.Meta.Result
	}
	metaDirRes, err := dagql.NewObjectResultForCurrentID(ctx, srv, metaDir)
	if err != nil {
		return nil, nil, nil, nil, 0, fmt.Errorf("failed to create meta directory: %w", err)
	}
	if err := addMount(ContainerMount{
		Target:          buildkit.MetaMountDestPath,
		DirectorySource: &metaDirRes,
	}); err != nil {
		return nil, nil, nil, nil, 0, err
	}

	// other normal mounts
	for _, mount := range container.Mounts {
		if err := addMount(mount); err != nil {
			return nil, nil, nil, nil, 0, err
		}
	}

	// handle secret mounts
	for _, secret := range container.Secrets {
		if secret.MountPath == "" {
			continue
		}
		uid, gid := 0, 0
		if secret.Owner != nil {
			uid, gid = secret.Owner.UID, secret.Owner.GID
		}
		mount := &pb.Mount{
			Input:     pb.Empty,
			Output:    pb.SkipOutput,
			Dest:      secret.MountPath,
			MountType: pb.MountType_SECRET,
			SecretOpt: &pb.SecretOpt{
				ID:   secret.Secret.ID().Digest().String(),
				Uid:  uint32(uid),
				Gid:  uint32(gid),
				Mode: uint32(secret.Mode),
			},
		}
		mounts = append(mounts, mount)
	}

	// handle socket mounts
	for _, socket := range container.Sockets {
		if socket.ContainerPath == "" {
			return nil, nil, nil, nil, 0, fmt.Errorf("unsupported socket: only unix paths are implemented")
		}
		uid, gid := 0, 0
		if socket.Owner != nil {
			uid, gid = socket.Owner.UID, socket.Owner.GID
		}
		mount := &pb.Mount{
			Input:     pb.Empty,
			Output:    pb.SkipOutput,
			Dest:      socket.ContainerPath,
			MountType: pb.MountType_SSH,
			SSHOpt: &pb.SSHOpt{
				ID:   socket.Source.LLBID(),
				Uid:  uint32(uid),
				Gid:  uint32(gid),
				Mode: 0o600, // preserve default
			},
		}
		mounts = append(mounts, mount)
	}

	return mounts, states, dgsts, refs, outputIdx, nil
}

// setAllContainerMounts is the reverse of getAllContainerMounts, and rewrites
// the container mounts to the given states.
func (op *ContainerDagOp) setAllContainerMounts(
	ctx context.Context,
	container *Container,
	outputs []llb.State,
) error {
	for mountIdx, mount := range op.Mounts {
		if mount.Output == pb.SkipOutput {
			continue
		}
		st := outputs[mount.Output]
		def, err := st.Marshal(ctx, llb.Platform(container.Platform.Spec()))
		if err != nil {
			return err
		}

		switch mountIdx {
		case 0:
			rootfsDir := &Directory{
				LLB: def.ToPB(),
			}
			if container.FS != nil {
				rootfsDir.Dir = container.FS.Self().Dir
				rootfsDir.Platform = container.FS.Self().Platform
				rootfsDir.Services = container.FS.Self().Services
			}
			container.FS, err = UpdatedRootFS(ctx, rootfsDir)
			if err != nil {
				return fmt.Errorf("failed to update rootfs: %w", err)
			}

		case 1:
			container.Meta = &Directory{
				LLB: def.ToPB(),
			}

		default:
			ctrMnt := container.Mounts[mountIdx-2]
			err := handleMountValue(ctrMnt,
				func(dirMnt *dagql.ObjectResult[*Directory]) error {
					dir := &Directory{
						LLB:      def.ToPB(),
						Dir:      ctrMnt.DirectorySource.Self().Dir,
						Platform: ctrMnt.DirectorySource.Self().Platform,
						Services: ctrMnt.DirectorySource.Self().Services,
					}
					ctrMnt.DirectorySource, err = updatedDirMount(ctx, dir, ctrMnt.Target)
					if err != nil {
						return fmt.Errorf("failed to update directory mount: %w", err)
					}
					container.Mounts[mountIdx-2] = ctrMnt
					return nil
				},
				func(fileMnt *dagql.ObjectResult[*File]) error {
					file := &File{
						LLB:      def.ToPB(),
						File:     ctrMnt.FileSource.Self().File,
						Platform: ctrMnt.FileSource.Self().Platform,
						Services: ctrMnt.FileSource.Self().Services,
					}
					ctrMnt.FileSource, err = updatedFileMount(ctx, file, ctrMnt.Target)
					if err != nil {
						return fmt.Errorf("failed to update file mount: %w", err)
					}
					container.Mounts[mountIdx-2] = ctrMnt
					return nil
				},
				func(cache *CacheMountSource) error {
					return fmt.Errorf("unhandled cache mount source type for mount %d", mountIdx)
				},
				func(tmpfs *TmpfsMountSource) error {
					return fmt.Errorf("unhandled tmpfs mount source type for mount %d", mountIdx)
				},
			)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// extractContainerBkOutputs returns a list of outputs suitable to be returned
// from CustomOp.Exec extracted from the container according to the dagop specification.
func extractContainerBkOutputs(ctx context.Context, container *Container, bk *buildkit.Client, wkr worker.Worker, mounts ContainerMountData) ([]solver.Result, error) {
	getResult := func(
		def *pb.Definition,
		ref bkcache.ImmutableRef,
		dgst digest.Digest,
	) (solver.Result, error) {
		switch {
		case ref != nil:
			ref = ref.Clone()
		case def != nil:
			res, err := bk.Solve(ctx, bkgw.SolveRequest{
				Evaluate:   true,
				Definition: def,
			})
			if err != nil {
				return nil, err
			}
			cachedRes, err := res.Ref.Result(ctx)
			if err != nil {
				return nil, err
			}
			ref = cachedRes.Sys().(*worker.WorkerRef).ImmutableRef
		default:
			return worker.NewWorkerRefResult(nil, wkr), nil
		}

		if ref != nil && dgst != "" {
			if err := ref.SetString(keyDaggerDigest, dgst.String(), daggerDigestIdx+dgst.String()); err != nil {
				return nil, fmt.Errorf("failed to set dagger digest on ref: %w", err)
			}
		}

		return worker.NewWorkerRefResult(ref, wkr), nil
	}

	outputs := make([]solver.Result, mounts.OutputCount)
	for mountIdx, mount := range mounts.Mounts {
		if mount.Output == pb.SkipOutput {
			continue
		}
		var ref solver.Result
		var err error
		switch mountIdx {
		case 0:
			ref, err = getResult(
				container.FS.Self().LLB,
				container.FS.Self().Result,
				container.FS.ID().Digest(),
			)
		case 1:
			var llb *pb.Definition
			var res bkcache.ImmutableRef
			if container.Meta != nil {
				llb = container.Meta.LLB
				res = container.Meta.Result
			}
			ref, err = getResult(llb, res, "")
		default:
			mnt := container.Mounts[mountIdx-2]
			switch {
			case mnt.DirectorySource != nil:
				ref, err = getResult(
					mnt.DirectorySource.Self().LLB,
					mnt.DirectorySource.Self().Result,
					mnt.DirectorySource.ID().Digest(),
				)
			case mnt.FileSource != nil:
				ref, err = getResult(
					mnt.FileSource.Self().LLB,
					mnt.FileSource.Self().Result,
					mnt.FileSource.ID().Digest(),
				)
			default:
				err = fmt.Errorf("mount %d has no source", mountIdx)
			}
		}
		if err != nil {
			return nil, err
		}
		outputs[mount.Output] = ref
	}

	return outputs, nil
}

func newDagOpLLB(ctx context.Context, dagOp buildkit.CustomOp, id *call.ID, inputs []llb.State) (llb.State, error) {
	return buildkit.NewCustomLLB(ctx, dagOp, inputs,
		llb.WithCustomNamef("%s %s", dagOp.Name(), id.Name()),
		buildkit.WithTracePropagation(ctx),
		buildkit.WithPassthrough(),
		llb.SkipEdgeMerge,
	)
}
