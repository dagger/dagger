package core

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	ctrdmount "github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	snapshot "github.com/dagger/dagger/engine/snapshots/snapshotter"
	"github.com/dagger/dagger/internal/buildkit/executor"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	utilsystem "github.com/dagger/dagger/internal/buildkit/util/system"
	"github.com/moby/sys/userns"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/network"
	"go.opentelemetry.io/otel/trace"
)

var ErrNoCommand = errors.New("no command has been set")
var ErrNoSvcCommand = errors.New("no service command has been set")

type ContainerExecOpts struct {
	// Command to run instead of the container's default command
	Args []string

	// If the container has an entrypoint, prepend it to this exec's args
	UseEntrypoint bool `default:"false"`

	// Content to write to the command's standard input before closing
	Stdin string `default:""`
	// Redirect the command's standard input from a file in the container
	RedirectStdin string `default:""`

	// Redirect the command's standard output to a file in the container
	RedirectStdout string `default:""`

	// Redirect the command's standard error to a file in the container
	RedirectStderr string `default:""`

	// Exit codes this exec is allowed to exit with
	Expect ReturnTypes `default:"SUCCESS"`

	// Provide the executed command access back to the Dagger API
	ExperimentalPrivilegedNesting bool `default:"false"`

	// Grant the process all root capabilities
	InsecureRootCapabilities bool `default:"false"`

	// (Internal-only) execute with no network connectivity, equivalent to
	// BuildKit NetMode_NONE / Dockerfile RUN --network=none.
	NoNetwork bool `internal:"true" default:"false"`

	// (Internal-only) execute with host networking, equivalent to
	// BuildKit NetMode_HOST / Dockerfile RUN --network=host.
	HostNetwork bool `internal:"true" default:"false"`

	// Expand the environment variables in args
	Expand bool `default:"false"`

	// Skip the init process injected into containers by default so that the
	// user's process is PID 1
	NoInit bool `default:"false"`
}

type ContainerExecState struct {
	LazyState

	Parent             dagql.ObjectResult[*Container]
	Opts               ContainerExecOpts
	ExecMD             *engineutil.ExecutionMetadata
	ExtractModuleError bool
}

type ContainerExecLazy struct {
	State *ContainerExecState
}

type persistedContainerExecLazy struct {
	ParentResultID     uint64                        `json:"parentResultID"`
	Opts               ContainerExecOpts             `json:"opts"`
	ExecMD             *engineutil.ExecutionMetadata `json:"execMD,omitempty"`
	ExtractModuleError bool                          `json:"extractModuleError,omitempty"`
}

func (lazy *ContainerExecLazy) Evaluate(ctx context.Context, ctr *Container) error {
	if lazy == nil || lazy.State == nil {
		return nil
	}
	return lazy.State.Evaluate(ctx, ctr)
}

func (lazy *ContainerExecLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	if lazy == nil || lazy.State == nil {
		return nil, nil
	}
	parent, err := attachContainerResult(attach, lazy.State.Parent, "attach container withExec parent")
	if err != nil {
		return nil, err
	}
	lazy.State.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (lazy *ContainerExecLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	if lazy == nil || lazy.State == nil {
		return nil, fmt.Errorf("encode persisted container withExec lazy: nil state")
	}
	parentID, err := encodePersistedObjectRef(cache, lazy.State.Parent, "container withExec parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerExecLazy{
		ParentResultID:     parentID,
		Opts:               lazy.State.Opts,
		ExecMD:             lazy.State.ExecMD,
		ExtractModuleError: lazy.State.ExtractModuleError,
	})
}

//nolint:gocyclo // intrinsically long state machine; refactoring would hurt clarity
func (container *Container) execMeta(ctx context.Context, opts ContainerExecOpts, parent *engineutil.ExecutionMetadata) (*engineutil.ExecutionMetadata, error) {
	execMD := engineutil.ExecutionMetadata{}
	if parent != nil {
		execMD = *parent
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	execMD.CallerClientID = clientMetadata.ClientID
	execMD.SessionID = clientMetadata.SessionID
	execMD.LockMode = clientMetadata.LockMode
	execMD.AllowedLLMModules = clientMetadata.AllowedLLMModules

	if execMD.Call == nil {
		execMD.Call = dagql.CurrentCall(ctx)
	}
	if execMD.CallDigest == "" && execMD.Call != nil {
		callDigest, err := execMD.Call.RecipeDigest(ctx)
		if err != nil {
			return nil, fmt.Errorf("compute exec call digest: %w", err)
		}
		execMD.CallDigest = callDigest
	}
	if execMD.HostAliases == nil {
		execMD.HostAliases = make(map[string][]string)
	}
	execMD.RedirectStdinPath = opts.RedirectStdin
	execMD.RedirectStdoutPath = opts.RedirectStdout
	execMD.RedirectStderrPath = opts.RedirectStderr
	execMD.SystemEnvNames = container.SystemEnvNames
	execMD.EnabledGPUs = container.EnabledGPUs
	if opts.NoInit {
		execMD.NoInit = true
	}

	var callerModDigest digest.Digest
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	if execMD.EncodedContentModuleID != "" {
		callerModID := new(call.ID)
		if err := callerModID.Decode(execMD.EncodedContentModuleID); err != nil {
			return nil, fmt.Errorf("failed to decode content-scoped module ID: %w", err)
		}
		callerMod, err := dagql.NewID[*Module](callerModID).Load(ctx, dag)
		if err != nil {
			return nil, fmt.Errorf("failed to load content-scoped module from encoded module ID: %w", err)
		}
		callerModDigest, err = callerMod.ContentPreferredDigest(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get content-scoped module digest: %w", err)
		}
	} else if execMD.EncodedModuleID != "" {
		callerModID := new(call.ID)
		if err := callerModID.Decode(execMD.EncodedModuleID); err != nil {
			return nil, fmt.Errorf("failed to decode module ID: %w", err)
		}
		callerMod, err := dagql.NewID[*Module](callerModID).Load(ctx, dag)
		if err != nil {
			return nil, fmt.Errorf("failed to load module from encoded module ID: %w", err)
		}
		implementationScopedMod, err := ImplementationScopedModule(ctx, callerMod)
		if err != nil {
			return nil, fmt.Errorf("failed to get implementation-scoped module from encoded module ID: %w", err)
		}
		callerModDigest, err = implementationScopedMod.ContentPreferredDigest(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get implementation-scoped module digest from encoded module ID: %w", err)
		}
	} else if callerMod, err := query.CurrentModule(ctx); err == nil && callerMod.Self() != nil {
		implementationScopedMod, err := ImplementationScopedModule(ctx, callerMod)
		if err != nil {
			return nil, fmt.Errorf("failed to get caller implementation-scoped module: %w", err)
		}
		callerModDigest, err = implementationScopedMod.ContentPreferredDigest(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get caller implementation-scoped module digest: %w", err)
		}
	}

	if callerModDigest != "" {
		// allow the exec to reach services scoped to the module that installed it
		execMD.ExtraSearchDomains = append(execMD.ExtraSearchDomains, network.ModuleDomain(callerModDigest, clientMetadata.SessionID))
	}

	// if GPU parameters are set for this container pass them over:
	if len(execMD.EnabledGPUs) > 0 {
		if gpuSupportEnabled := os.Getenv("_EXPERIMENTAL_DAGGER_GPU_SUPPORT"); gpuSupportEnabled == "" {
			return nil, fmt.Errorf("GPU support is not enabled, set _EXPERIMENTAL_DAGGER_GPU_SUPPORT")
		}
	}

	// this allows executed containers to communicate back to this API
	if opts.ExperimentalPrivilegedNesting {
		// establish new client ID for the nested client
		if execMD.ClientID == "" {
			execMD.ClientID = identity.NewID()
		}
	}

	for _, bnd := range container.Services {
		for _, alias := range bnd.Aliases {
			execMD.HostAliases[bnd.Hostname] = append(execMD.HostAliases[bnd.Hostname], alias)
		}
	}

	for i, secret := range container.Secrets {
		switch {
		case secret.EnvName != "":
			execMD.SecretEnvNames = append(execMD.SecretEnvNames, secret.EnvName)
		case secret.MountPath != "":
			execMD.SecretFilePaths = append(execMD.SecretFilePaths, secret.MountPath)
		default:
			return nil, fmt.Errorf("malformed secret config at index %d", i)
		}
	}

	return &execMD, nil
}

func (container *Container) metaSpec(ctx context.Context, opts ContainerExecOpts) (*executor.Meta, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current query: %w", err)
	}

	cfg := container.Config
	args, err := container.command(opts)
	if err != nil {
		return nil, err
	}
	platform := container.Platform
	if platform.OS == "" {
		platform = query.Platform()
	}

	metaSpec := executor.Meta{
		Args:                      args,
		Env:                       slices.Clone(cfg.Env),
		Cwd:                       cmp.Or(cfg.WorkingDir, "/"),
		User:                      cfg.User,
		RemoveMountStubsRecursive: true,
	}
	if opts.InsecureRootCapabilities {
		metaSpec.SecurityMode = pb.SecurityMode_INSECURE
	}
	netMode, err := execNetMode(opts)
	if err != nil {
		return nil, err
	}
	if netMode != pb.NetMode_UNSET {
		metaSpec.NetMode = netMode
	}

	metaSpec.Env = addDefaultEnvvar(metaSpec.Env, "PATH", utilsystem.DefaultPathEnv(platform.OS))

	if opts.Expect != ReturnSuccess {
		metaSpec.ValidExitCodes = opts.Expect.ReturnCodes()
	}

	return &metaSpec, nil
}

func execNetMode(opts ContainerExecOpts) (pb.NetMode, error) {
	if opts.NoNetwork && opts.HostNetwork {
		return pb.NetMode_UNSET, fmt.Errorf("cannot set both noNetwork and hostNetwork")
	}
	if opts.NoNetwork {
		return pb.NetMode_NONE, nil
	}
	if opts.HostNetwork {
		return pb.NetMode_HOST, nil
	}
	return pb.NetMode_UNSET, nil
}

func (container *Container) secretEnvValues(ctx context.Context) ([]string, error) {
	env := make([]string, 0, len(container.Secrets))
	for _, secret := range container.Secrets {
		if secret.EnvName == "" {
			continue
		}
		plaintext, err := secret.Secret.Self().Plaintext(ctx)
		if err != nil {
			return nil, fmt.Errorf("secret env %q: %w", secret.EnvName, err)
		}
		env = append(env, secret.EnvName+"="+string(plaintext))
	}
	return env, nil
}

type execSecretMountConfig struct {
	Secret dagql.ObjectResult[*Secret]
	UID    int
	GID    int
	Mode   fs.FileMode
}

type execSSHMountConfig struct {
	Socket dagql.ObjectResult[*Socket]
	UID    int
	GID    int
	Mode   fs.FileMode
}

type execMountState struct {
	Dest      string
	Selector  string
	Readonly  bool
	MountType pb.MountType

	SourceRef bkcache.Ref

	TmpfsOpt *pb.TmpfsOpt
	Secret   *execSecretMountConfig
	SSH      *execSSHMountConfig

	ApplyOutput func(bkcache.ImmutableRef) error

	ActiveRef       bkcache.MutableRef
	OutputMutable   bkcache.MutableRef
	OutputImmutable bkcache.ImmutableRef
}

type materializedExecPlan struct {
	Root   executor.Mount
	Mounts []executor.Mount
	States []*execMountState
}

func lockMountedCaches(ctx context.Context, mounts []ContainerMount) (func(), error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current query for cache locks: %w", err)
	}
	locker := query.Locker()
	if locker == nil {
		return nil, fmt.Errorf("missing engine locker")
	}

	lockSet := make(map[string]struct{})
	for i, ctrMount := range mounts {
		if ctrMount.Readonly || ctrMount.CacheSource == nil || ctrMount.CacheSource.Volume.Self() == nil {
			continue
		}
		cacheSelf := ctrMount.CacheSource.Volume.Self()
		if cacheSelf.Sharing != CacheSharingModeLocked {
			continue
		}
		payload, err := cacheSelf.EncodePersistedObject(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("encode cache lock key for mount %d: %w", i, err)
		}
		lockSet["cache-volume:"+string(payload)] = struct{}{}
	}
	if len(lockSet) == 0 {
		return func() {}, nil
	}

	lockKeys := make([]string, 0, len(lockSet))
	for lockKey := range lockSet {
		lockKeys = append(lockKeys, lockKey)
	}
	sort.Strings(lockKeys)
	for _, lockKey := range lockKeys {
		locker.Lock(lockKey)
	}
	return func() {
		for i := len(lockKeys) - 1; i >= 0; i-- {
			locker.Unlock(lockKeys[i])
		}
	}, nil
}

func (plan *materializedExecPlan) releaseActives(ctx context.Context) error {
	var rerr error
	for i := len(plan.States) - 1; i >= 0; i-- {
		active := plan.States[i].ActiveRef
		if active == nil {
			continue
		}
		rerr = errors.Join(rerr, active.Release(ctx))
		plan.States[i].ActiveRef = nil
	}
	return rerr
}

func (plan *materializedExecPlan) releaseOutputRefs(ctx context.Context) error {
	var rerr error
	for _, state := range plan.States {
		if state.OutputMutable != nil {
			rerr = errors.Join(rerr, state.OutputMutable.Release(ctx))
			state.OutputMutable = nil
		}
		if state.OutputImmutable != nil {
			rerr = errors.Join(rerr, state.OutputImmutable.Release(ctx))
			state.OutputImmutable = nil
		}
	}
	return rerr
}

type makeExecMutable func(dest string, ref bkcache.ImmutableRef) (bkcache.MutableRef, error)

//nolint:dupl,gocyclo // symmetric with the mount-materialization closure inside (*ContainerExecState).Evaluate; sharing hurts readability of each phase
func prepareMounts(
	ctx context.Context,
	container *Container,
	rootOutput func(bkcache.ImmutableRef) error,
	metaOutput func(bkcache.ImmutableRef) error,
	mountOutputs []func(bkcache.ImmutableRef) error,
	cache bkcache.SnapshotManager,
	cwd string,
	platform string,
	makeMutable makeExecMutable,
) (materialized materializedExecPlan, err error) {
	defer func() {
		if err != nil {
			_ = materialized.releaseActives(context.WithoutCancel(ctx))
			_ = materialized.releaseOutputRefs(context.WithoutCancel(ctx))
		}
	}()

	materializeState := func(state *execMountState) error {
		var mountable bkcache.Mountable
		if state.SourceRef != nil {
			mountable = state.SourceRef
		}

		switch state.MountType {
		case pb.MountType_BIND:
			if state.ApplyOutput != nil {
				if state.Readonly && state.SourceRef != nil && state.Dest != pb.RootMount {
					iref, ok := state.SourceRef.(bkcache.ImmutableRef)
					if !ok {
						return fmt.Errorf("mount %s readonly output needs immutable input, got %T", state.Dest, state.SourceRef)
					}
					reopened, err := cache.GetBySnapshotID(ctx, iref.SnapshotID(), bkcache.NoUpdateLastUsed)
					if err != nil {
						return err
					}
					state.OutputImmutable = reopened
				} else {
					iref, ok := state.SourceRef.(bkcache.ImmutableRef)
					if state.SourceRef != nil && !ok {
						return fmt.Errorf("mount %s writable output needs immutable input, got %T", state.Dest, state.SourceRef)
					}
					active, err := makeMutable(state.Dest, iref)
					if err != nil {
						return err
					}
					mountable = active
					state.OutputMutable = active
				}
			} else {
				if !state.Readonly && state.SourceRef != nil {
					if mutable, ok := state.SourceRef.(bkcache.MutableRef); ok {
						mountable = mutable
					} else {
						iref := state.SourceRef.(bkcache.ImmutableRef)
						active, err := makeMutable(state.Dest, iref)
						if err != nil {
							return err
						}
						mountable = active
						state.ActiveRef = active
					}
				} else if !state.Readonly || state.SourceRef == nil {
					var iref bkcache.ImmutableRef
					if state.SourceRef != nil {
						parsed, ok := state.SourceRef.(bkcache.ImmutableRef)
						if !ok {
							return fmt.Errorf("mount %s writable bind needs immutable or mutable input, got %T", state.Dest, state.SourceRef)
						}
						iref = parsed
					}
					active, err := makeMutable(state.Dest, iref)
					if err != nil {
						return err
					}
					mountable = active
					state.ActiveRef = active
				}
			}

		case pb.MountType_TMPFS:
			mountable = execTmpFSMountable(state.TmpfsOpt)

		case pb.MountType_SECRET:
			mountable, err = prepareExecSecretMount(ctx, state.Secret)
			if err != nil {
				return err
			}

		case pb.MountType_SSH:
			mountable, err = prepareExecSSHMount(state.SSH)
			if err != nil {
				return err
			}

		default:
			return fmt.Errorf("mount type %s not implemented", state.MountType)
		}

		if state.Dest == pb.RootMount && state.Readonly && state.ApplyOutput == nil {
			if _, ok := mountable.(bkcache.MutableRef); !ok {
				var iref bkcache.ImmutableRef
				if state.SourceRef != nil {
					parsed, ok := state.SourceRef.(bkcache.ImmutableRef)
					if !ok {
						return fmt.Errorf("root mount readonly fallback needs immutable input, got %T", state.SourceRef)
					}
					iref = parsed
				}
				active, err := makeMutable(state.Dest, iref)
				if err != nil {
					return err
				}
				mountable = active
				state.ActiveRef = active
			}
		}

		if mountable == nil {
			return nil
		}

		if state.Dest == pb.RootMount {
			root := execMount(mountable)
			root.Selector = state.Selector
			materialized.Root = root
		} else {
			mount := execMount(mountable)
			dest := state.Dest
			if !utilsystem.IsAbs(filepath.Clean(dest), platform) {
				dest = filepath.Join("/", cwd, dest)
			}
			mount.Dest = dest
			mount.Readonly = state.Readonly
			mount.Selector = state.Selector
			materialized.Mounts = append(materialized.Mounts, mount)
		}

		materialized.States = append(materialized.States, state)
		return nil
	}

	rootState := &execMountState{
		Dest:        pb.RootMount,
		Selector:    "/",
		MountType:   pb.MountType_BIND,
		ApplyOutput: rootOutput,
	}
	if container.FS != nil {
		if rootfs, ok := container.FS.Peek(); ok && rootfs != nil {
			if selector, ok := rootfs.Dir.Peek(); ok && selector != "" {
				rootState.Selector = selector
			}
			if ref, ok := rootfs.Snapshot.Peek(); ok && ref != nil {
				rootState.SourceRef = ref
			}
		}
	}
	if err := materializeState(rootState); err != nil {
		return materialized, err
	}

	metaState := &execMountState{
		Dest:        engineutil.MetaMountDestPath,
		Selector:    "/",
		MountType:   pb.MountType_BIND,
		ApplyOutput: metaOutput,
	}
	if container.MetaSnapshot != nil {
		if metaRef, ok := container.MetaSnapshot.Peek(); ok && metaRef != nil {
			metaState.SourceRef = metaRef
		}
	}
	if err := materializeState(metaState); err != nil {
		return materialized, err
	}

	for i, ctrMount := range container.Mounts {
		mountState := &execMountState{
			Dest:      ctrMount.Target,
			MountType: pb.MountType_BIND,
			Readonly:  ctrMount.Readonly,
		}

		switch {
		case ctrMount.DirectorySource != nil:
			dir, ok := ctrMount.DirectorySource.Peek()
			if !ok || dir == nil {
				return materialized, fmt.Errorf("mount %d has nil directory source", i)
			}
			if selector, ok := dir.Dir.Peek(); ok {
				mountState.Selector = selector
			}
			ref, ok := dir.Snapshot.Peek()
			if !ok {
				return materialized, fmt.Errorf("failed to get directory snapshot for mount %d", i)
			}
			mountState.SourceRef = ref

		case ctrMount.FileSource != nil:
			file, ok := ctrMount.FileSource.Peek()
			if !ok || file == nil {
				return materialized, fmt.Errorf("mount %d has nil file source", i)
			}
			if selector, ok := file.File.Peek(); ok {
				mountState.Selector = selector
			}
			ref, ok := file.Snapshot.Peek()
			if !ok {
				return materialized, fmt.Errorf("failed to get file snapshot for mount %d", i)
			}
			mountState.SourceRef = ref

		case ctrMount.CacheSource != nil:
			cacheSrc := ctrMount.CacheSource
			if cacheSrc.Volume.Self() == nil {
				return materialized, fmt.Errorf("mount %d has nil cache volume source", i)
			}
			if cacheSrc.Volume.Self().getSnapshot() == nil {
				if err := cacheSrc.Volume.Self().InitializeSnapshot(ctx); err != nil {
					return materialized, fmt.Errorf("initialize cache volume snapshot for mount %d: %w", i, err)
				}
			}
			cacheSnapshot := cacheSrc.Volume.Self().getSnapshot()
			if cacheSnapshot == nil {
				return materialized, fmt.Errorf("mount %d has nil cache volume snapshot", i)
			}
			mountState.SourceRef = cacheSnapshot
			mountState.Selector = cacheSrc.Volume.Self().getSnapshotSelector()

		case ctrMount.TmpfsSource != nil:
			mountState.MountType = pb.MountType_TMPFS
			mountState.TmpfsOpt = &pb.TmpfsOpt{
				Size_: int64(ctrMount.TmpfsSource.Size),
			}

		default:
			return materialized, fmt.Errorf("mount %d has no source", i)
		}

		if i < len(mountOutputs) {
			if output := mountOutputs[i]; output != nil && !mountState.Readonly {
				mountState.ApplyOutput = output
			}
		}
		if err := materializeState(mountState); err != nil {
			return materialized, err
		}
	}

	for _, secret := range container.Secrets {
		if secret.MountPath == "" {
			continue
		}
		uid, gid := 0, 0
		if secret.Owner != nil {
			uid, gid = secret.Owner.UID, secret.Owner.GID
		}
		secretState := &execMountState{
			Dest:      secret.MountPath,
			MountType: pb.MountType_SECRET,
			Secret: &execSecretMountConfig{
				Secret: secret.Secret,
				UID:    uid,
				GID:    gid,
				Mode:   secret.Mode,
			},
		}
		if err := materializeState(secretState); err != nil {
			return materialized, err
		}
	}

	for i, socket := range container.Sockets {
		if socket.ContainerPath == "" {
			return materialized, fmt.Errorf("unsupported socket %d: only unix paths are implemented", i)
		}
		uid, gid := 0, 0
		if socket.Owner != nil {
			uid, gid = socket.Owner.UID, socket.Owner.GID
		}
		socketState := &execMountState{
			Dest:      socket.ContainerPath,
			MountType: pb.MountType_SSH,
			SSH: &execSSHMountConfig{
				Socket: socket.Source,
				UID:    uid,
				GID:    gid,
				Mode:   0o600,
			},
		}
		if err := materializeState(socketState); err != nil {
			return materialized, err
		}
	}

	sort.Slice(materialized.Mounts, func(i, j int) bool {
		return materialized.Mounts[i].Dest < materialized.Mounts[j].Dest
	})

	return materialized, nil
}

func execMount(mountable bkcache.Mountable) executor.Mount {
	_, readonly := mountable.(bkcache.ImmutableRef)
	return executor.Mount{
		Src:      mountable,
		Readonly: readonly,
	}
}

func execTmpFSMountable(opt *pb.TmpfsOpt) bkcache.Mountable {
	return &execTmpFS{opt: opt}
}

type execTmpFS struct {
	opt *pb.TmpfsOpt
}

func (tmpfs *execTmpFS) Mount(_ context.Context, readonly bool) (snapshot.Mountable, error) {
	return &execTmpFSMount{
		readonly: readonly,
		opt:      tmpfs.opt,
	}, nil
}

type execTmpFSMount struct {
	readonly bool
	opt      *pb.TmpfsOpt
}

func (tmpfs *execTmpFSMount) Mount() ([]ctrdmount.Mount, func() error, error) {
	options := []string{"nosuid"}
	if tmpfs.readonly {
		options = append(options, "ro")
	}
	if tmpfs.opt != nil && tmpfs.opt.Size_ > 0 {
		options = append(options, fmt.Sprintf("size=%d", tmpfs.opt.Size_))
	}
	return []ctrdmount.Mount{{
		Type:    "tmpfs",
		Source:  "tmpfs",
		Options: options,
	}}, func() error { return nil }, nil
}

func prepareExecSecretMount(ctx context.Context, cfg *execSecretMountConfig) (bkcache.Mountable, error) {
	if cfg == nil {
		return nil, fmt.Errorf("invalid secret mount options")
	}
	if cfg.Secret.Self() == nil {
		return nil, fmt.Errorf("secret mount missing secret")
	}

	data, err := cfg.Secret.Self().Plaintext(ctx)
	if err != nil {
		return nil, err
	}

	return &execSecretMount{
		uid:  cfg.UID,
		gid:  cfg.GID,
		mode: cfg.Mode,
		data: data,
	}, nil
}

type execSecretMount struct {
	uid  int
	gid  int
	mode fs.FileMode
	data []byte
}

func (secret *execSecretMount) Mount(_ context.Context, _ bool) (snapshot.Mountable, error) {
	return &execSecretMountInstance{
		secret: secret,
	}, nil
}

type execSecretMountInstance struct {
	secret *execSecretMount
}

func (secret *execSecretMountInstance) Mount() ([]ctrdmount.Mount, func() error, error) {
	dir, err := os.MkdirTemp("", "buildkit-secrets")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	cleanupDir := func() error {
		return os.RemoveAll(dir)
	}

	if err := os.Chmod(dir, 0o711); err != nil {
		_ = cleanupDir()
		return nil, nil, err
	}

	var mountOpts []string
	if secret.secret.mode&0o111 == 0 {
		mountOpts = append(mountOpts, "noexec")
	}

	tmpMount := ctrdmount.Mount{
		Type:    "tmpfs",
		Source:  "tmpfs",
		Options: append([]string{"nodev", "nosuid", fmt.Sprintf("uid=%d,gid=%d", os.Geteuid(), os.Getegid())}, mountOpts...),
	}
	if userns.RunningInUserNS() {
		tmpMount.Options = nil
	}

	if err := ctrdmount.All([]ctrdmount.Mount{tmpMount}, dir); err != nil {
		_ = cleanupDir()
		return nil, nil, fmt.Errorf("unable to setup secret mount: %w", err)
	}

	cleanup := func() error {
		if err := ctrdmount.Unmount(dir, 0); err != nil {
			return err
		}
		return cleanupDir()
	}

	fp := filepath.Join(dir, identity.NewID())
	if err := os.WriteFile(fp, secret.secret.data, 0o600); err != nil {
		_ = cleanup()
		return nil, nil, err
	}

	if err := os.Chown(fp, secret.secret.uid, secret.secret.gid); err != nil {
		_ = cleanup()
		return nil, nil, err
	}
	if err := os.Chmod(fp, secret.secret.mode&0o777); err != nil {
		_ = cleanup()
		return nil, nil, err
	}

	return []ctrdmount.Mount{{
		Type:    "bind",
		Source:  fp,
		Options: append([]string{"ro", "rbind", "nodev", "nosuid"}, mountOpts...),
	}}, cleanup, nil
}

func prepareExecSSHMount(cfg *execSSHMountConfig) (bkcache.Mountable, error) {
	if cfg == nil {
		return nil, fmt.Errorf("invalid ssh mount options")
	}
	if cfg.Socket.Self() == nil {
		return nil, fmt.Errorf("ssh mount missing socket")
	}

	return &execSSHMount{
		socket: cfg.Socket,
		uid:    cfg.UID,
		gid:    cfg.GID,
		mode:   cfg.Mode,
	}, nil
}

type execSSHMount struct {
	socket dagql.ObjectResult[*Socket]
	uid    int
	gid    int
	mode   fs.FileMode
}

func (ssh *execSSHMount) Mount(ctx context.Context, _ bool) (snapshot.Mountable, error) {
	sock, cleanup, err := ssh.socket.Self().MountSSHAgent(ctx)
	if err != nil {
		return nil, err
	}
	return &execSSHMountInstance{
		ssh:     ssh,
		sock:    sock,
		cleanup: cleanup,
	}, nil
}

type execSSHMountInstance struct {
	ssh     *execSSHMount
	sock    string
	cleanup func() error
}

func (ssh *execSSHMountInstance) Mount() ([]ctrdmount.Mount, func() error, error) {
	if err := os.Chown(ssh.sock, ssh.ssh.uid, ssh.ssh.gid); err != nil {
		if ssh.cleanup != nil {
			_ = ssh.cleanup()
		}
		return nil, nil, err
	}
	if err := os.Chmod(ssh.sock, ssh.ssh.mode&0o777); err != nil {
		if ssh.cleanup != nil {
			_ = ssh.cleanup()
		}
		return nil, nil, err
	}
	release := func() error {
		if ssh.cleanup == nil {
			return nil
		}
		return ssh.cleanup()
	}

	return []ctrdmount.Mount{{
		Type:    "bind",
		Source:  ssh.sock,
		Options: []string{"rbind"},
	}}, release, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithExec(
	ctx context.Context,
	parent dagql.ObjectResult[*Container],
	opts ContainerExecOpts,
	execMD *engineutil.ExecutionMetadata,
	extractModuleError bool,
) error {
	state := &ContainerExecState{
		LazyState:          NewLazyState(),
		Parent:             parent,
		Opts:               opts,
		ExecMD:             execMD,
		ExtractModuleError: extractModuleError,
	}
	container.Lazy = &ContainerExecLazy{State: state}
	container.ImageRef = ""
	container.MetaSnapshot = new(LazyAccessor[bkcache.ImmutableRef, *Container])
	container.FS = new(LazyAccessor[*Directory, *Container])

	for i, ctrMount := range container.Mounts {
		if ctrMount.Readonly {
			continue
		}
		switch {
		case ctrMount.DirectorySource != nil:
			ctrMount.DirectorySource = new(LazyAccessor[*Directory, *Container])
			container.Mounts[i] = ctrMount
		case ctrMount.FileSource != nil:
			ctrMount.FileSource = new(LazyAccessor[*File, *Container])
			container.Mounts[i] = ctrMount
		}
	}
	return nil
}

//nolint:dupl,gocyclo // symmetric with prepareMounts; sharing hurts readability of each phase
func (state *ContainerExecState) Evaluate(ctx context.Context, container *Container) (rerr error) {
	if state == nil {
		return nil
	}

	return state.LazyState.Evaluate(ctx, "Container.withExec", func(ctx context.Context) (rerr error) {
		dagCache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := dagCache.Evaluate(ctx, state.Parent); err != nil {
			return err
		}
		if err := materializeContainerStateFromParent(ctx, container, state.Parent); err != nil {
			return err
		}

		parent := state.Parent.Self()
		if parent == nil {
			return fmt.Errorf("exec parent is nil")
		}
		if container == nil {
			return fmt.Errorf("exec output container is nil")
		}
		inputRootFS := container.FS
		inputMounts := slices.Clone(container.Mounts)

		query, err := CurrentQuery(ctx)
		if err != nil {
			return fmt.Errorf("get current query: %w", err)
		}
		releaseLockedCaches, err := lockMountedCaches(ctx, inputMounts)
		if err != nil {
			return err
		}
		defer releaseLockedCaches()

		secretEnv, err := container.secretEnvValues(ctx)
		if err != nil {
			return err
		}

		opts := state.Opts
		expandedArgs := make([]string, len(opts.Args))
		for i, arg := range opts.Args {
			expandedArg, err := expandContainerInput(container, arg, opts.Expand)
			if err != nil {
				return err
			}
			expandedArgs[i] = expandedArg
		}
		opts.Args = expandedArgs

		if opts.RedirectStdout != "" {
			path, err := resolveContainerInputPath(container, opts.RedirectStdout, opts.Expand)
			if err != nil {
				return err
			}
			opts.RedirectStdout = path
		}
		if opts.RedirectStderr != "" {
			path, err := resolveContainerInputPath(container, opts.RedirectStderr, opts.Expand)
			if err != nil {
				return err
			}
			opts.RedirectStderr = path
		}
		if opts.RedirectStdin != "" {
			path, err := resolveContainerInputPath(container, opts.RedirectStdin, opts.Expand)
			if err != nil {
				return err
			}
			opts.RedirectStdin = path
		}

		execMD, err := container.execMeta(ctx, opts, state.ExecMD)
		if err != nil {
			return err
		}
		state.ExecMD = execMD

		cache := query.SnapshotManager()

		metaSpec, err := container.metaSpec(ctx, opts)
		if err != nil {
			return err
		}

		engineClient, err := query.Engine(ctx)
		if err != nil {
			return fmt.Errorf("failed to get engine client: %w", err)
		}
		opWorker := engineClient.Worker
		causeCtx := trace.SpanContextFromContext(ctx)
		if opWorker == nil {
			return fmt.Errorf("missing buildkit worker")
		}

		rootOutputBinding := func(ref bkcache.ImmutableRef) error {
			dirPath := "/"
			platform := container.Platform
			services := slices.Clone(container.Services)
			if inputRootFS != nil {
				if rootfs, ok := inputRootFS.Peek(); ok && rootfs != nil {
					if path, ok := rootfs.Dir.Peek(); ok && path != "" {
						dirPath = path
					}
					platform = rootfs.Platform
					services = slices.Clone(rootfs.Services)
				}
			}
			output := &Directory{
				Platform: platform,
				Services: services,
				Dir:      new(LazyAccessor[string, *Directory]),
				Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
			}
			output.Dir.setValue(dirPath)
			output.Snapshot.setValue(ref)
			if container.FS == nil {
				container.FS = new(LazyAccessor[*Directory, *Container])
			}
			container.FS.setValue(output)
			return nil
		}
		metaOutputBinding := func(ref bkcache.ImmutableRef) error {
			if container.MetaSnapshot == nil {
				container.MetaSnapshot = new(LazyAccessor[bkcache.ImmutableRef, *Container])
			}
			container.MetaSnapshot.setValue(ref)
			return nil
		}
		mountOutputBindings := make([]func(bkcache.ImmutableRef) error, len(container.Mounts))
		for i, ctrMount := range inputMounts {
			if ctrMount.Readonly {
				continue
			}
			switch {
			case ctrMount.DirectorySource != nil:
				inputDir, ok := ctrMount.DirectorySource.Peek()
				if !ok || inputDir == nil {
					return fmt.Errorf("exec directory mount %d is missing materialized input", i)
				}
				dirPath, _ := inputDir.Dir.Peek()
				platform := inputDir.Platform
				services := slices.Clone(inputDir.Services)
				idx := i
				mountOutputBindings[i] = func(ref bkcache.ImmutableRef) error {
					output := &Directory{
						Platform: platform,
						Services: slices.Clone(services),
						Dir:      new(LazyAccessor[string, *Directory]),
						Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
					}
					output.Dir.setValue(dirPath)
					output.Snapshot.setValue(ref)
					if container.Mounts[idx].DirectorySource == nil {
						container.Mounts[idx].DirectorySource = new(LazyAccessor[*Directory, *Container])
					}
					container.Mounts[idx].DirectorySource.setValue(output)
					return nil
				}
			case ctrMount.FileSource != nil:
				inputFile, ok := ctrMount.FileSource.Peek()
				if !ok || inputFile == nil {
					return fmt.Errorf("exec file mount %d is missing materialized input", i)
				}
				filePath, _ := inputFile.File.Peek()
				platform := inputFile.Platform
				services := slices.Clone(inputFile.Services)
				idx := i
				mountOutputBindings[i] = func(ref bkcache.ImmutableRef) error {
					output := &File{
						Platform: platform,
						Services: slices.Clone(services),
						File:     new(LazyAccessor[string, *File]),
						Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]),
					}
					output.File.setValue(filePath)
					output.Snapshot.setValue(ref)
					if container.Mounts[idx].FileSource == nil {
						container.Mounts[idx].FileSource = new(LazyAccessor[*File, *Container])
					}
					container.Mounts[idx].FileSource.setValue(output)
					return nil
				}
			}
		}

		var rootMount executor.Mount
		execMounts := []executor.Mount{}
		mountStates := []*execMountState{}
		releaseActives := func() error {
			var releaseErr error
			for i := len(mountStates) - 1; i >= 0; i-- {
				active := mountStates[i].ActiveRef
				if active == nil {
					continue
				}
				releaseErr = errors.Join(releaseErr, active.Release(context.WithoutCancel(ctx)))
				mountStates[i].ActiveRef = nil
			}
			return releaseErr
		}
		releaseOutputRefs := func() error {
			var releaseErr error
			for _, state := range mountStates {
				if state.OutputMutable != nil {
					releaseErr = errors.Join(releaseErr, state.OutputMutable.Release(context.WithoutCancel(ctx)))
					state.OutputMutable = nil
				}
				if state.OutputImmutable != nil {
					releaseErr = errors.Join(releaseErr, state.OutputImmutable.Release(context.WithoutCancel(ctx)))
					state.OutputImmutable = nil
				}
			}
			return releaseErr
		}
		applyOutputs := func() error {
			for _, state := range mountStates {
				if state.ApplyOutput == nil {
					continue
				}

				if state.OutputMutable != nil {
					committed, err := state.OutputMutable.Commit(ctx)
					if err != nil {
						return fmt.Errorf("error committing %s: %w", state.OutputMutable.ID(), err)
					}
					state.OutputMutable = nil
					state.OutputImmutable = committed
				}

				if state.OutputImmutable == nil {
					continue
				}

				if err := state.ApplyOutput(state.OutputImmutable); err != nil {
					return err
				}
				state.OutputImmutable = nil
			}
			return nil
		}

		makeMutable := func(dest string, ref bkcache.ImmutableRef) (bkcache.MutableRef, error) {
			desc := fmt.Sprintf("mount %s from exec %s", dest, strings.Join(metaSpec.Args, " "))
			return cache.New(ctx, ref, bkcache.WithDescription(desc))
		}
		materializeState := func(state *execMountState) error {
			var mountable bkcache.Mountable
			if state.SourceRef != nil {
				mountable = state.SourceRef
			}

			switch state.MountType {
			case pb.MountType_BIND:
				if state.ApplyOutput != nil {
					if state.Readonly && state.SourceRef != nil && state.Dest != pb.RootMount {
						iref, ok := state.SourceRef.(bkcache.ImmutableRef)
						if !ok {
							return fmt.Errorf("mount %s readonly output needs immutable input, got %T", state.Dest, state.SourceRef)
						}
						reopened, err := cache.GetBySnapshotID(ctx, iref.SnapshotID(), bkcache.NoUpdateLastUsed)
						if err != nil {
							return err
						}
						state.OutputImmutable = reopened
					} else {
						iref, ok := state.SourceRef.(bkcache.ImmutableRef)
						if state.SourceRef != nil && !ok {
							return fmt.Errorf("mount %s writable output needs immutable input, got %T", state.Dest, state.SourceRef)
						}
						active, err := makeMutable(state.Dest, iref)
						if err != nil {
							return err
						}
						mountable = active
						state.OutputMutable = active
					}
				} else {
					if !state.Readonly && state.SourceRef != nil {
						if mutable, ok := state.SourceRef.(bkcache.MutableRef); ok {
							mountable = mutable
						} else {
							iref := state.SourceRef.(bkcache.ImmutableRef)
							active, err := makeMutable(state.Dest, iref)
							if err != nil {
								return err
							}
							mountable = active
							state.ActiveRef = active
						}
					} else if !state.Readonly || state.SourceRef == nil {
						var iref bkcache.ImmutableRef
						if state.SourceRef != nil {
							parsed, ok := state.SourceRef.(bkcache.ImmutableRef)
							if !ok {
								return fmt.Errorf("mount %s writable bind needs immutable or mutable input, got %T", state.Dest, state.SourceRef)
							}
							iref = parsed
						}
						active, err := makeMutable(state.Dest, iref)
						if err != nil {
							return err
						}
						mountable = active
						state.ActiveRef = active
					}
				}

			case pb.MountType_TMPFS:
				mountable = execTmpFSMountable(state.TmpfsOpt)

			case pb.MountType_SECRET:
				mountable, err = prepareExecSecretMount(ctx, state.Secret)
				if err != nil {
					return err
				}

			case pb.MountType_SSH:
				mountable, err = prepareExecSSHMount(state.SSH)
				if err != nil {
					return err
				}

			default:
				return fmt.Errorf("mount type %s not implemented", state.MountType)
			}

			if state.Dest == pb.RootMount && state.Readonly && state.ApplyOutput == nil {
				if _, ok := mountable.(bkcache.MutableRef); !ok {
					var iref bkcache.ImmutableRef
					if state.SourceRef != nil {
						parsed, ok := state.SourceRef.(bkcache.ImmutableRef)
						if !ok {
							return fmt.Errorf("root mount readonly fallback needs immutable input, got %T", state.SourceRef)
						}
						iref = parsed
					}
					active, err := makeMutable(state.Dest, iref)
					if err != nil {
						return err
					}
					mountable = active
					state.ActiveRef = active
				}
			}

			if mountable == nil {
				return nil
			}

			if state.Dest == pb.RootMount {
				root := execMount(mountable)
				root.Selector = state.Selector
				rootMount = root
			} else {
				mount := execMount(mountable)
				dest := state.Dest
				if !utilsystem.IsAbs(filepath.Clean(dest), runtime.GOOS) {
					dest = filepath.Join("/", container.Config.WorkingDir, dest)
				}
				mount.Dest = dest
				mount.Readonly = state.Readonly
				mount.Selector = state.Selector
				execMounts = append(execMounts, mount)
			}

			mountStates = append(mountStates, state)
			return nil
		}
		failPrepare := func(err error) error {
			_ = releaseActives()
			_ = releaseOutputRefs()
			return fmt.Errorf("failed to prepare mounts: %w", err)
		}

		rootState := &execMountState{
			Dest:        pb.RootMount,
			Selector:    "/",
			MountType:   pb.MountType_BIND,
			ApplyOutput: rootOutputBinding,
		}
		if inputRootFS != nil {
			if rootfs, ok := inputRootFS.Peek(); ok && rootfs != nil {
				if selector, ok := rootfs.Dir.Peek(); ok && selector != "" {
					rootState.Selector = selector
				}
				ref, ok := rootfs.Snapshot.Peek()
				if !ok {
					return failPrepare(fmt.Errorf("parent rootfs should already be materialized after parent evaluation"))
				}
				rootState.SourceRef = ref
			}
		}
		if err := materializeState(rootState); err != nil {
			return failPrepare(err)
		}

		metaState := &execMountState{
			Dest:        engineutil.MetaMountDestPath,
			Selector:    "/",
			MountType:   pb.MountType_BIND,
			ApplyOutput: metaOutputBinding,
		}
		if err := materializeState(metaState); err != nil {
			return failPrepare(err)
		}

		for i, ctrMount := range inputMounts {
			mountState := &execMountState{
				Dest:      ctrMount.Target,
				MountType: pb.MountType_BIND,
				Readonly:  ctrMount.Readonly,
			}

			switch {
			case ctrMount.DirectorySource != nil:
				dir, ok := ctrMount.DirectorySource.Peek()
				if !ok || dir == nil {
					return failPrepare(fmt.Errorf("mount %d has nil directory source", i))
				}
				if selector, ok := dir.Dir.Peek(); ok {
					mountState.Selector = selector
				}
				ref, ok := dir.Snapshot.Peek()
				if !ok {
					return failPrepare(fmt.Errorf("parent directory mount %d should already be materialized after parent evaluation", i))
				}
				mountState.SourceRef = ref

			case ctrMount.FileSource != nil:
				file, ok := ctrMount.FileSource.Peek()
				if !ok || file == nil {
					return failPrepare(fmt.Errorf("mount %d has nil file source", i))
				}
				if selector, ok := file.File.Peek(); ok {
					mountState.Selector = selector
				}
				ref, ok := file.Snapshot.Peek()
				if !ok {
					return failPrepare(fmt.Errorf("parent file mount %d should already be materialized after parent evaluation", i))
				}
				mountState.SourceRef = ref

			case ctrMount.CacheSource != nil:
				cacheSrc := ctrMount.CacheSource
				if cacheSrc.Volume.Self() == nil {
					return failPrepare(fmt.Errorf("mount %d has nil cache volume source", i))
				}
				if cacheSrc.Volume.Self().getSnapshot() == nil {
					if err := cacheSrc.Volume.Self().InitializeSnapshot(ctx); err != nil {
						return failPrepare(fmt.Errorf("initialize cache volume snapshot for mount %d: %w", i, err))
					}
				}
				cacheSnapshot := cacheSrc.Volume.Self().getSnapshot()
				if cacheSnapshot == nil {
					return failPrepare(fmt.Errorf("mount %d has nil cache volume snapshot", i))
				}
				mountState.SourceRef = cacheSnapshot
				mountState.Selector = cacheSrc.Volume.Self().getSnapshotSelector()

			case ctrMount.TmpfsSource != nil:
				mountState.MountType = pb.MountType_TMPFS
				mountState.TmpfsOpt = &pb.TmpfsOpt{
					Size_: int64(ctrMount.TmpfsSource.Size),
				}

			default:
				return failPrepare(fmt.Errorf("mount %d has no source", i))
			}

			if output := mountOutputBindings[i]; output != nil && !mountState.Readonly {
				mountState.ApplyOutput = output
			}
			if err := materializeState(mountState); err != nil {
				return failPrepare(err)
			}
		}

		for _, secret := range container.Secrets {
			if secret.MountPath == "" {
				continue
			}
			uid, gid := 0, 0
			if secret.Owner != nil {
				uid, gid = secret.Owner.UID, secret.Owner.GID
			}
			secretState := &execMountState{
				Dest:      secret.MountPath,
				MountType: pb.MountType_SECRET,
				Secret: &execSecretMountConfig{
					Secret: secret.Secret,
					UID:    uid,
					GID:    gid,
					Mode:   secret.Mode,
				},
			}
			if err := materializeState(secretState); err != nil {
				return failPrepare(err)
			}
		}

		for i, socket := range container.Sockets {
			if socket.ContainerPath == "" {
				return failPrepare(fmt.Errorf("unsupported socket %d: only unix paths are implemented", i))
			}
			uid, gid := 0, 0
			if socket.Owner != nil {
				uid, gid = socket.Owner.UID, socket.Owner.GID
			}
			socketState := &execMountState{
				Dest:      socket.ContainerPath,
				MountType: pb.MountType_SSH,
				SSH: &execSSHMountConfig{
					Socket: socket.Source,
					UID:    uid,
					GID:    gid,
					Mode:   0o600,
				},
			}
			if err := materializeState(socketState); err != nil {
				return failPrepare(err)
			}
		}

		sort.Slice(execMounts, func(i, j int) bool {
			return execMounts[i].Dest < execMounts[j].Dest
		})

		defer func() {
			_ = releaseActives()
			_ = releaseOutputRefs()
		}()

		defer func() {
			if rerr == nil {
				return
			}

			resolveFailureRef := func(state *execMountState) (bkcache.ImmutableRef, error) {
				switch {
				case state.OutputImmutable != nil:
					ref := state.OutputImmutable
					state.OutputImmutable = nil
					return ref, nil
				case state.OutputMutable != nil:
					iref, err := state.OutputMutable.Commit(ctx)
					if err != nil {
						return nil, fmt.Errorf("commit output ref %s: %w", state.OutputMutable.ID(), err)
					}
					state.OutputMutable = nil
					return iref, nil
				case state.ActiveRef != nil:
					iref, err := state.ActiveRef.Commit(ctx)
					if err != nil {
						return nil, fmt.Errorf("commit active ref %s: %w", state.ActiveRef.ID(), err)
					}
					state.ActiveRef = nil
					return iref, nil
				default:
					sourceRef, ok := state.SourceRef.(bkcache.ImmutableRef)
					if ok && sourceRef != nil {
						return cache.GetBySnapshotID(ctx, sourceRef.SnapshotID(), bkcache.NoUpdateLastUsed)
					}
					return nil, nil
				}
			}

			resolvedRefs := []bkcache.ImmutableRef{}
			trackResolvedRef := func(ref bkcache.ImmutableRef) bkcache.ImmutableRef {
				if ref != nil {
					resolvedRefs = append(resolvedRefs, ref)
				}
				return ref
			}
			untrackResolvedRef := func(target bkcache.ImmutableRef) {
				for i, ref := range resolvedRefs {
					if ref == target {
						resolvedRefs = append(resolvedRefs[:i], resolvedRefs[i+1:]...)
						return
					}
				}
			}
			releaseResolvedRefs := func() {
				for _, ref := range resolvedRefs {
					if ref == nil {
						continue
					}
					_ = ref.Release(context.WithoutCancel(ctx))
				}
			}
			defer releaseResolvedRefs()

			var metaRef bkcache.ImmutableRef
			var moduleRef bkcache.ImmutableRef
			var moduleErrID dagql.ID[*Error]
			haveModuleErrID := false
			resolveAndTrackFailureRef := func(state *execMountState) (bkcache.ImmutableRef, error) {
				ref, err := resolveFailureRef(state)
				if err != nil {
					return nil, err
				}
				return trackResolvedRef(ref), nil
			}
			for _, mountState := range mountStates {
				keepRef := mountState.Dest == engineutil.MetaMountDestPath
				if state.ExtractModuleError && mountState.Dest == modMetaDirPath {
					keepRef = true
				}
				if !keepRef {
					continue
				}
				ref, err := resolveAndTrackFailureRef(mountState)
				if err != nil {
					rerr = errors.Join(rerr, err)
					continue
				}
				if ref == nil {
					continue
				}
				if mountState.Dest == engineutil.MetaMountDestPath {
					metaRef = ref
				}
				if state.ExtractModuleError && mountState.Dest == modMetaDirPath {
					moduleRef = ref
				}
			}

			if state.ExtractModuleError && moduleRef != nil {
				errID, ok, err := moduleErrorIDFromRef(ctx, engineClient, moduleRef)
				if err != nil {
					rerr = errors.Join(rerr, fmt.Errorf("extract module error: %w", err))
					return
				}
				if ok {
					moduleErrID = errID
					haveModuleErrID = true
				}
			}

			execMDPresent := execMD != nil
			execInternal := false
			hasMetaSpec := metaSpec != nil
			if execMDPresent {
				execInternal = execMD.Internal
			}
			if engineClient.Interactive &&
				execMDPresent &&
				!execInternal &&
				hasMetaSpec {
				var callID *call.ID
				var callDig digest.Digest
				if execMD.Call != nil {
					dagqlCache, err := dagql.EngineCache(ctx)
					if err != nil {
						rerr = fmt.Errorf("get dagql cache for terminal exec error: %w", err)
						return
					}
					callID, err = dagqlCache.RecipeIDForCall(ctx, execMD.Call)
					if err != nil {
						rerr = fmt.Errorf("rebuild recipe ID for terminal exec error: %w", err)
						return
					}
					// Failed interactive execs spawn a throwaway terminal service.
					// The service manager needs a digest-shaped key for hostname/log
					// plumbing, but it must not participate in DAG/content identity.
					callDig = digest.FromString(rand.Text())
				}
				meta := *metaSpec
				meta.Args = []string{"/bin/sh"}
				if len(engineClient.InteractiveCommand) > 0 {
					meta.Args = engineClient.InteractiveCommand
				}
				var terminalContainer *Container
				terminalContainerNeedsRelease := false
				defer func() {
					if terminalContainerNeedsRelease && terminalContainer != nil {
						_ = terminalContainer.OnRelease(context.WithoutCancel(ctx))
					}
				}()
				terminalContainer, err = cloneContainerForTerminal(ctx, query, container)
				if err != nil {
					rerr = fmt.Errorf("clone terminal container: %w", err)
					return
				}
				terminalContainer.FS, err = cloneTerminalDirectorySource(ctx, query, inputRootFS)
				if err != nil {
					rerr = fmt.Errorf("clone terminal rootfs source: %w", err)
					return
				}
				terminalContainer.Mounts, err = cloneTerminalMounts(ctx, query, inputMounts)
				if err != nil {
					rerr = fmt.Errorf("clone terminal mounts: %w", err)
					return
				}
				if metaRef != nil {
					terminalMetaRef, err := query.SnapshotManager().GetBySnapshotID(
						ctx,
						metaRef.SnapshotID(),
						bkcache.NoUpdateLastUsed,
					)
					if err != nil {
						rerr = fmt.Errorf("reopen meta snapshot for terminal: %w", err)
						return
					}
					trackResolvedRef(terminalMetaRef)
					if terminalContainer.MetaSnapshot == nil {
						terminalContainer.MetaSnapshot = new(LazyAccessor[bkcache.ImmutableRef, *Container])
					}
					terminalContainer.MetaSnapshot.setValue(terminalMetaRef)
					untrackResolvedRef(terminalMetaRef)
					terminalContainerNeedsRelease = true
				}
				if len(mountStates) >= 1 {
					rootRef, err := resolveAndTrackFailureRef(mountStates[0])
					if err != nil {
						rerr = fmt.Errorf("resolve failed rootfs for terminal: %w", err)
						return
					}
					if rootRef != nil {
						rootDirPath := "/"
						rootPlatform := terminalContainer.Platform
						rootServices := slices.Clone(terminalContainer.Services)
						if inputRootFS != nil {
							if rootfs, ok := inputRootFS.Peek(); ok && rootfs != nil {
								if path, ok := rootfs.Dir.Peek(); ok && path != "" {
									rootDirPath = path
								}
								rootPlatform = rootfs.Platform
								rootServices = slices.Clone(rootfs.Services)
							}
						}
						rootDir := &Directory{
							Platform: rootPlatform,
							Services: rootServices,
							Dir:      new(LazyAccessor[string, *Directory]),
							Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
						}
						rootDir.Dir.setValue(rootDirPath)
						rootDir.Snapshot.setValue(rootRef)
						untrackResolvedRef(rootRef)
						if terminalContainer.FS == nil {
							terminalContainer.FS = new(LazyAccessor[*Directory, *Container])
						}
						terminalContainer.FS.setValue(rootDir)
						terminalContainerNeedsRelease = true
					}
				}
				for i, ctrMount := range inputMounts {
					stateIdx := i + 2
					if stateIdx >= len(mountStates) {
						break
					}
					mountRef, err := resolveAndTrackFailureRef(mountStates[stateIdx])
					if err != nil {
						rerr = fmt.Errorf("resolve failed mount %d for terminal: %w", i, err)
						return
					}
					if mountRef == nil {
						continue
					}
					switch {
					case ctrMount.DirectorySource != nil && !ctrMount.Readonly:
						inputDir, ok := ctrMount.DirectorySource.Peek()
						if !ok || inputDir == nil {
							continue
						}
						dirPath, _ := inputDir.Dir.Peek()
						outputDir := &Directory{
							Platform: inputDir.Platform,
							Services: slices.Clone(inputDir.Services),
							Dir:      new(LazyAccessor[string, *Directory]),
							Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
						}
						outputDir.Dir.setValue(dirPath)
						outputDir.Snapshot.setValue(mountRef)
						untrackResolvedRef(mountRef)
						if ctrMount.DirectorySource == nil {
							ctrMount.DirectorySource = new(LazyAccessor[*Directory, *Container])
						}
						ctrMount.DirectorySource.setValue(outputDir)
						terminalContainer.Mounts[i] = ctrMount
						terminalContainerNeedsRelease = true
					case ctrMount.FileSource != nil && !ctrMount.Readonly:
						inputFile, ok := ctrMount.FileSource.Peek()
						if !ok || inputFile == nil {
							continue
						}
						filePath, _ := inputFile.File.Peek()
						outputFile := &File{
							Platform: inputFile.Platform,
							Services: slices.Clone(inputFile.Services),
							File:     new(LazyAccessor[string, *File]),
							Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]),
						}
						outputFile.File.setValue(filePath)
						outputFile.Snapshot.setValue(mountRef)
						untrackResolvedRef(mountRef)
						if ctrMount.FileSource == nil {
							ctrMount.FileSource = new(LazyAccessor[*File, *Container])
						}
						ctrMount.FileSource.setValue(outputFile)
						terminalContainer.Mounts[i] = ctrMount
						terminalContainerNeedsRelease = true
					}
				}
				srv, err := CurrentDagqlServer(ctx)
				if err != nil {
					rerr = err
					return
				}
				terminalContainerRes, err := newSyntheticTerminalContainerResult(srv, terminalContainer, "terminal_exec_error_container")
				if err != nil {
					rerr = err
					return
				}
				if err := terminalContainer.TerminalExecError(ctx, callID, callDig, terminalContainerRes, execMD, &meta, rerr); err != nil {
					rerr = err
					return
				}
				terminalContainerNeedsRelease = false
			}

			var existingExecErr *ExecError
			if !errors.As(rerr, &existingExecErr) {
				execErr, ok, err := execErrorFromMetaRef(ctx, engineClient, causeCtx, rerr, metaSpec, metaRef)
				if err != nil {
					rerr = errors.Join(err, rerr)
					return
				}
				if ok {
					rerr = execErr
				}
			}

			if haveModuleErrID {
				rerr = &ModuleExecError{
					Err:     rerr,
					ErrorID: moduleErrID,
				}
			}
		}()

		emu, err := getEmulator(ctx, specs.Platform(container.Platform))
		if err != nil {
			return err
		}
		if emu != nil {
			metaSpec.Args = append([]string{engineutil.BuildkitQemuEmulatorMountPoint}, metaSpec.Args...)
			execMounts = append(execMounts, executor.Mount{
				Readonly: true,
				Src:      emu,
				Dest:     engineutil.BuildkitQemuEmulatorMountPoint,
			})
		}

		meta := *metaSpec
		meta.Env = slices.Clone(meta.Env)
		meta.Env = append(meta.Env, secretEnv...)

		svcs, err := query.Services(ctx)
		if err != nil {
			return fmt.Errorf("failed to get services: %w", err)
		}
		detach, _, err := svcs.StartBindings(ctx, container.Services)
		if err != nil {
			return err
		}
		defer detach()

		execWorker := opWorker.ExecWorker(causeCtx, *execMD)
		procInfo := executor.ProcessInfo{Meta: meta}
		if opts.Stdin != "" {
			procInfo.Stdin = io.NopCloser(strings.NewReader(opts.Stdin))
		}
		_, execErr := execWorker.Run(ctx, "", rootMount, execMounts, procInfo, nil)

		var invalidateErr error
		for i, ctrMount := range inputMounts {
			if ctrMount.Readonly || ctrMount.CacheSource == nil || ctrMount.CacheSource.Volume.Self() == nil {
				continue
			}
			if err := ctrMount.CacheSource.Volume.Self().invalidateSnapshotSize(ctx); err != nil {
				invalidateErr = errors.Join(invalidateErr, fmt.Errorf("invalidate cache mount %d size: %w", i, err))
			}
		}

		if execErr != nil {
			slog.Warn("process did not complete successfully",
				"process", strings.Join(metaSpec.Args, " "),
				"error", execErr)
			if invalidateErr != nil {
				return errors.Join(execErr, invalidateErr)
			}
			return execErr
		}
		if invalidateErr != nil {
			return invalidateErr
		}
		if err := applyOutputs(); err != nil {
			return err
		}

		container.Lazy = nil
		return nil
	})
}

func decodePersistedContainerExecLazy(
	ctx context.Context,
	dag *dagql.Server,
	container *Container,
	payload json.RawMessage,
	decodedRootFS decodedContainerDirectoryValue,
	decodedMounts []decodedContainerMount,
) error {
	var persisted persistedContainerExecLazy
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return fmt.Errorf("decode persisted container withExec lazy payload: %w", err)
	}
	parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container exec parent")
	if err != nil {
		return err
	}
	state := &ContainerExecState{
		LazyState:          NewLazyState(),
		Parent:             parent,
		Opts:               persisted.Opts,
		ExecMD:             persisted.ExecMD,
		ExtractModuleError: persisted.ExtractModuleError,
	}
	container.Lazy = &ContainerExecLazy{State: state}
	container.ImageRef = ""
	container.MetaSnapshot = new(LazyAccessor[bkcache.ImmutableRef, *Container])
	if decodedRootFS.Kind == persistedContainerValueFormPending {
		container.FS = new(LazyAccessor[*Directory, *Container])
	}
	for i, decodedMount := range decodedMounts {
		if container.Mounts[i].Readonly || decodedMount.Kind != persistedContainerValueFormPending {
			continue
		}
		switch {
		case container.Mounts[i].DirectorySource != nil:
			container.Mounts[i].DirectorySource = new(LazyAccessor[*Directory, *Container])
		case container.Mounts[i].FileSource != nil:
			container.Mounts[i].FileSource = new(LazyAccessor[*File, *Container])
		}
	}
	return nil
}

func addDefaultEnvvar(env []string, k, v string) []string {
	for _, e := range env {
		if strings.HasPrefix(e, k+"=") {
			return env
		}
	}
	return append(env, k+"="+v)
}

func (container *Container) Stdout(ctx context.Context) (string, error) {
	return container.metaFileContents(ctx, engineutil.MetaMountStdoutPath)
}

func (container *Container) Stderr(ctx context.Context) (string, error) {
	return container.metaFileContents(ctx, engineutil.MetaMountStderrPath)
}

func (container *Container) CombinedOutput(ctx context.Context) (string, error) {
	return container.metaFileContents(ctx, engineutil.MetaMountCombinedOutputPath)
}

func (container *Container) ExitCode(ctx context.Context) (int, error) {
	contents, err := container.metaFileContents(ctx, engineutil.MetaMountExitCodePath)
	if err != nil {
		return 0, err
	}
	contents = strings.TrimSpace(contents)

	code, err := strconv.ParseInt(contents, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("could not parse exit code %q: %w", contents, err)
	}

	return int(code), nil
}

func (container *Container) metaFileContents(ctx context.Context, filePath string) (string, error) {
	if err := container.Evaluate(ctx); err != nil {
		return "", err
	}

	if container.MetaSnapshot == nil {
		return "", ErrNoCommand
	}
	metaSnapshot, ok := container.MetaSnapshot.Peek()
	if !ok || metaSnapshot == nil {
		return "", ErrNoCommand
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	reopened, err := query.SnapshotManager().GetBySnapshotID(ctx, metaSnapshot.SnapshotID(), bkcache.NoUpdateLastUsed)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = reopened.Release(context.WithoutCancel(ctx))
	}()

	var content []byte
	err = MountRef(ctx, reopened, func(root string, _ *ctrdmount.Mount) error {
		fullPath, err := containerdfs.RootPath(root, filePath)
		if err != nil {
			return err
		}
		content, err = os.ReadFile(fullPath)
		if err != nil {
			return TrimErrPathPrefix(err, root)
		}
		return nil
	}, mountRefAsReadOnly)
	if err != nil {
		if errors.Is(err, errEmptyResultRef) {
			return "", ErrNoCommand
		}
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNoCommand
		}
		return "", err
	}
	return string(content), nil
}
