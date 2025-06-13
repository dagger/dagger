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
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/snapshot"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
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
) (*Directory, error) {
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

	// Data is any additional data that should be passed to the dagop.
	Data string
}

func (op FSDagOp) Name() string {
	return "dagop.fs"
}

func (op FSDagOp) Backend() buildkit.CustomOpBackend {
	return &op
}

func (op FSDagOp) Digest() (digest.Digest, error) {
	opData, err := json.Marshal(op.Data)
	if err != nil {
		return "", err
	}
	return digest.FromString(strings.Join([]string{
		op.ID.Digest().String(),
		op.Path,
		string(opData),
	}, "+")), nil
}

func (op FSDagOp) CacheMap(ctx context.Context, cm *solver.CacheMap) (*solver.CacheMap, error) {
	cm.Digest = digest.FromString(strings.Join([]string{
		op.ID.Digest().String(),
		op.Path,
	}, "+"))
	return cm, nil
}

func (op FSDagOp) Exec(ctx context.Context, g bksession.Group, inputs []solver.Result, opt buildkit.OpOpts) (outputs []solver.Result, err error) {
	query, ok := opt.Server.Root().(dagql.Instance[*Query])
	if !ok {
		return nil, fmt.Errorf("server root was %T", opt.Server.Root())
	}
	obj, err := opt.Server.LoadType(ContextWithQuery(ctx, query.Self), op.ID)
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

func (op RawDagOp) CacheMap(ctx context.Context, cm *solver.CacheMap) (*solver.CacheMap, error) {
	cm.Digest = digest.FromString(strings.Join([]string{
		op.ID.Digest().String(),
		op.Filename,
	}, "+"))
	return cm, nil
}

func (op RawDagOp) Exec(ctx context.Context, g bksession.Group, inputs []solver.Result, opt buildkit.OpOpts) (outputs []solver.Result, retErr error) {
	query, ok := opt.Server.Root().(dagql.Instance[*Query])
	if !ok {
		return nil, fmt.Errorf("server root was %T", opt.Server.Root())
	}
	result, err := opt.Server.LoadType(ContextWithQuery(ctx, query.Self), op.ID)
	if err != nil {
		return nil, err
	}
	if wrapped, ok := result.(dagql.Wrapper); ok {
		result = wrapped.Unwrap()
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

func NewContainerDagOp(
	ctx context.Context,
	id *call.ID,
	ctr *Container,
	extraInputs []llb.State,
) (*Container, error) {
	mounts, inputs, outputCount, err := getAllContainerMounts(ctr)
	if err != nil {
		return nil, err
	}
	inputs = append(inputs, extraInputs...)

	dagop := &ContainerDagOp{
		ID:          id,
		Mounts:      mounts,
		OutputCount: outputCount,
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
	ID *call.ID

	// all the container mounts - the order here should be guaranteed:
	// - rootfs is at 0
	// - meta mount is at 1
	// - nth container.Mounts is at n+2
	// - secret/socket mounts are at the very end
	Mounts []*pb.Mount

	// the number of outputs produced
	OutputCount int

	// utility values set in the context of an Exec
	g   bksession.Group
	opt buildkit.OpOpts

	// inputs are all the inputs provided to the op
	//
	// be careful accessing it directly (stable order is not guaranteed, and it
	// may also contain a bunch of other stuff), ideally access it through a
	// known pb.Mount.Output index.
	inputs []solver.Result
}

func (op ContainerDagOp) Inputs() []bkcache.ImmutableRef {
	refs := make([]bkcache.ImmutableRef, 0, len(op.inputs))
	for _, input := range op.inputs {
		refs = append(refs, input.Sys().(*worker.WorkerRef).ImmutableRef)
	}
	return refs
}

func (op ContainerDagOp) Name() string {
	return "dagop.ctr"
}

func (op ContainerDagOp) Backend() buildkit.CustomOpBackend {
	return &op
}

func (op ContainerDagOp) Group() bksession.Group {
	return op.g
}

func (op ContainerDagOp) Digest() (digest.Digest, error) {
	mountsData, err := json.Marshal(op.Mounts)
	if err != nil {
		return "", err
	}
	return digest.FromString(strings.Join([]string{
		op.ID.Digest().String(),
		fmt.Sprint(op.OutputCount),
		string(mountsData),
	}, "+")), nil
}

func (op ContainerDagOp) CacheMap(ctx context.Context, cm *solver.CacheMap) (*solver.CacheMap, error) {
	// TODO: we need proper cache map control here, to control content digesting
	dgst, err := op.Digest()
	if err != nil {
		return nil, err
	}
	cm.Digest = dgst
	return cm, nil
}

func (op ContainerDagOp) Exec(ctx context.Context, g bksession.Group, inputs []solver.Result, opt buildkit.OpOpts) (outputs []solver.Result, retErr error) {
	op.g = g
	op.opt = opt
	op.inputs = inputs

	query, ok := opt.Server.Root().(dagql.Instance[*Query])
	if !ok {
		return nil, fmt.Errorf("server root was %T", opt.Server.Root())
	}
	obj, err := opt.Server.LoadType(ContextWithQuery(ctx, query.Self), op.ID)
	if err != nil {
		return nil, err
	}

	bk, err := query.Self.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	switch inst := obj.(type) {
	case dagql.Instance[*Container]:
		return op.extractContainerBkOutputs(ctx, inst.Self, bk, opt.Worker)
	default:
		// shouldn't happen, should have errored in DagLLB already
		return nil, fmt.Errorf("expected FS to be selected, instead got %T", obj)
	}
}

// getAllContainerMounts gets the list of all mounts for a container, as well as all the
// inputs that are part of it, and the total number of outputs. Each mount's
// Input maps to an index in the returned states.
func getAllContainerMounts(container *Container) (mounts []*pb.Mount, states []llb.State, outputCount int, _ error) {
	outputIdx := 0
	inputIdxs := map[digest.Digest]pb.InputIndex{}

	// addMount converts a ContainerMount and creates a corresponding buildkit
	// mount, creating an input if required
	addMount := func(mnt ContainerMount) error {
		st, err := defToState(mnt.Source)
		if err != nil {
			return err
		}

		mount := &pb.Mount{
			Dest:     mnt.Target,
			Selector: mnt.SourcePath,
			Output:   pb.OutputIndex(outputIdx),
			// ContentCache: nil,
		}
		if st.Output() == nil {
			mount.Input = pb.Empty
		} else {
			dag, err := buildkit.DefToDAG(mnt.Source)
			if err != nil {
				return err
			}

			if idx, ok := inputIdxs[*dag.OpDigest]; ok {
				mount.Input = idx
			} else {
				// track and cache this input index, since duplicates are unnecessary
				// also buildkit's FileOp (which is underlying our DagOp) will
				// remove them if we don't, which results in significant confusion
				mount.Input = pb.InputIndex(len(states))
				inputIdxs[*dag.OpDigest] = mount.Input
				states = append(states, st)
			}
		}

		if mnt.Readonly {
			mount.Output = pb.SkipOutput
			mount.Readonly = true
		}

		if mnt.CacheVolumeID != "" {
			mount.Output = pb.SkipOutput
			mount.MountType = pb.MountType_CACHE
			mount.CacheOpt = &pb.CacheOpt{
				ID: mnt.CacheVolumeID,
			}
			switch mnt.CacheSharingMode {
			case CacheSharingModeShared:
				mount.CacheOpt.Sharing = pb.CacheSharingOpt_SHARED
			case CacheSharingModePrivate:
				mount.CacheOpt.Sharing = pb.CacheSharingOpt_PRIVATE
			case CacheSharingModeLocked:
				mount.CacheOpt.Sharing = pb.CacheSharingOpt_LOCKED
			}
		}

		if mnt.Tmpfs {
			mount.Output = pb.SkipOutput
			mount.MountType = pb.MountType_TMPFS
			mount.TmpfsOpt = &pb.TmpfsOpt{
				Size_: int64(mnt.Size),
			}
		}

		mounts = append(mounts, mount)
		if mount.Output != pb.SkipOutput {
			outputIdx++
		}

		return nil
	}

	// handle our normal mounts
	if err := addMount(ContainerMount{Source: container.FS, Target: "/"}); err != nil {
		return nil, nil, 0, err
	}
	if err := addMount(ContainerMount{Source: container.Meta, Target: buildkit.MetaMountDestPath, SourcePath: buildkit.MetaMountDestPath}); err != nil {
		return nil, nil, 0, err
	}
	for _, mount := range container.Mounts {
		if err := addMount(mount); err != nil {
			return nil, nil, 0, err
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
			return nil, nil, 0, fmt.Errorf("unsupported socket: only unix paths are implemented")
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

	return mounts, states, outputIdx, nil
}

// setAllContainerMounts is the reverse of getAllContainerMounts, and rewrites
// the container mounts to the given states.
func (op *ContainerDagOp) setAllContainerMounts(ctx context.Context, container *Container, outputs []llb.State) error {
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
			container.FS = def.ToPB()
		case 1:
			container.Meta = def.ToPB()
		default:
			container.Mounts[mountIdx-2].Source = def.ToPB()
		}
	}

	return nil
}

// extractContainerBkOutputs returns a list of outputs suitable to be returned
// from CustomOp.Exec extracted from the container according to the dagop specification.
func (op *ContainerDagOp) extractContainerBkOutputs(ctx context.Context, container *Container, bk *buildkit.Client, wkr worker.Worker) ([]solver.Result, error) {
	getResult := func(def *pb.Definition, ref bkcache.ImmutableRef) (solver.Result, error) {
		if ref != nil {
			return worker.NewWorkerRefResult(ref.Clone(), wkr), nil
		}
		if def != nil {
			res, err := bk.Solve(ctx, bkgw.SolveRequest{
				Evaluate:   true,
				Definition: def,
			})
			if err != nil {
				return nil, err
			}
			ref, err := res.Ref.Result(ctx)
			if err != nil {
				return nil, err
			}
			if ref != nil {
				return worker.NewWorkerRefResult(ref.Sys().(*worker.WorkerRef).ImmutableRef, wkr), nil
			}
		}
		return worker.NewWorkerRefResult(nil, wkr), nil
	}

	outputs := make([]solver.Result, op.OutputCount)
	for mountIdx, mount := range op.Mounts {
		if mount.Output == pb.SkipOutput {
			continue
		}
		var ref solver.Result
		var err error
		switch mountIdx {
		case 0:
			ref, err = getResult(container.FS, container.FSResult)
		case 1:
			ref, err = getResult(container.Meta, container.MetaResult)
		default:
			mnt := container.Mounts[mountIdx-2]
			ref, err = getResult(mnt.Source, mnt.Result)
		}
		if err != nil {
			return nil, err
		}
		outputs[mount.Output] = ref
	}
	for i, output := range outputs {
		if output == nil {
			// this *shouldn't* happen, and means we've got somehow got gaps in
			// the output araray. the mounts are therefore badly constructed,
			// so we should error out. otherwise we'll get weird panics deep in
			// buildkit that are near impossible to debug.
			return nil, fmt.Errorf("internal: output %d was empty", i)
		}
	}

	return outputs, nil
}

func newDagOpLLB(ctx context.Context, dagOp buildkit.CustomOp, id *call.ID, inputs []llb.State) (llb.State, error) {
	return buildkit.NewCustomLLB(ctx, dagOp, inputs,
		llb.WithCustomNamef("%s %s", dagOp.Name(), id.Name()),
		buildkit.WithTracePropagation(ctx),
		buildkit.WithPassthrough(),
	)
}
