package core

import (
	"cmp"
	"context"
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

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/executor"
	"github.com/dagger/dagger/internal/buildkit/identity"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/snapshot"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	utilsystem "github.com/dagger/dagger/internal/buildkit/util/system"
	"github.com/docker/docker/pkg/idtools"
	"github.com/moby/sys/userns"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
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
	ExecMD             *buildkit.ExecutionMetadata
	ExtractModuleError bool

	Container *Container
}

type ContainerExecLazy struct {
	State *ContainerExecState
}

type persistedContainerExecLazy struct {
	ParentResultID     uint64                      `json:"parentResultID"`
	Opts               ContainerExecOpts           `json:"opts"`
	ExecMD             *buildkit.ExecutionMetadata `json:"execMD,omitempty"`
	ExtractModuleError bool                        `json:"extractModuleError,omitempty"`
}

func (lazy *ContainerExecLazy) Evaluate(ctx context.Context, ctr *Container) error {
	if lazy == nil || lazy.State == nil {
		return nil
	}
	return lazy.State.Evaluate(ctx)
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

func (container *Container) execMeta(ctx context.Context, opts ContainerExecOpts, parent *buildkit.ExecutionMetadata) (*buildkit.ExecutionMetadata, error) {
	execMD := buildkit.ExecutionMetadata{}
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
	if execMD.ExecID == "" {
		execMD.ExecID = identity.NewID()
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

	ActiveRef bkcache.MutableRef
	OutputRef bkcache.Ref
}

type materializedExecPlan struct {
	Root   executor.Mount
	Mounts []executor.Mount
	States []*execMountState
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
		if state.OutputRef == nil {
			continue
		}
		rerr = errors.Join(rerr, state.OutputRef.Release(ctx))
		state.OutputRef = nil
	}
	return rerr
}

type makeExecMutable func(dest string, ref bkcache.ImmutableRef) (bkcache.MutableRef, error)

func prepareMounts(
	ctx context.Context,
	container *Container,
	rootOutput func(bkcache.ImmutableRef) error,
	metaOutput func(bkcache.ImmutableRef) error,
	mountOutputs []func(bkcache.ImmutableRef) error,
	cache bkcache.SnapshotManager,
	session *bksession.Manager,
	g bksession.Group,
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
					state.OutputRef = iref.Clone()
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
					state.OutputRef = active
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
			mountable = execTmpFSMountable(cache, state.TmpfsOpt)

		case pb.MountType_SECRET:
			mountable, err = prepareExecSecretMount(ctx, cache, state.Secret)
			if err != nil {
				return err
			}

		case pb.MountType_SSH:
			mountable, err = prepareExecSSHMount(ctx, cache, state.SSH)
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
			root := execMountWithSession(mountable, g)
			root.Selector = state.Selector
			materialized.Root = root
		} else {
			mount := execMountWithSession(mountable, g)
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

	dagCache, err := dagql.EngineCache(ctx)
	if err != nil {
		return materialized, err
	}

	rootState := &execMountState{
		Dest:        pb.RootMount,
		Selector:    "/",
		MountType:   pb.MountType_BIND,
		ApplyOutput: rootOutput,
	}
	if container.FS != nil && container.FS.self() != nil {
		if container.FS.isResultBacked() {
			if err := dagCache.Evaluate(ctx, *container.FS.Result); err != nil {
				return materialized, fmt.Errorf("evaluate rootfs source: %w", err)
			}
		}
		rootState.Selector = container.FS.self().Dir
		if rootState.Selector == "" {
			rootState.Selector = "/"
		}
		ref, err := container.FS.self().getSnapshot()
		if err != nil {
			return materialized, fmt.Errorf("failed to get rootfs snapshot: %w", err)
		}
		rootState.SourceRef = ref
	}
	if err := materializeState(rootState); err != nil {
		return materialized, err
	}

	metaState := &execMountState{
		Dest:        buildkit.MetaMountDestPath,
		Selector:    "/",
		MountType:   pb.MountType_BIND,
		ApplyOutput: metaOutput,
	}
	if container.MetaSnapshot != nil {
		metaState.SourceRef = container.MetaSnapshot
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
			if ctrMount.DirectorySource.self() == nil {
				return materialized, fmt.Errorf("mount %d has nil directory source", i)
			}
			if ctrMount.DirectorySource.isResultBacked() {
				if err := dagCache.Evaluate(ctx, *ctrMount.DirectorySource.Result); err != nil {
					return materialized, fmt.Errorf("evaluate directory source for mount %d: %w", i, err)
				}
			}
			mountState.Selector = ctrMount.DirectorySource.self().Dir
			ref, err := ctrMount.DirectorySource.self().getSnapshot()
			if err != nil {
				return materialized, fmt.Errorf("failed to get directory snapshot for mount %d: %w", i, err)
			}
			mountState.SourceRef = ref

		case ctrMount.FileSource != nil:
			if ctrMount.FileSource.self() == nil {
				return materialized, fmt.Errorf("mount %d has nil file source", i)
			}
			if ctrMount.FileSource.isResultBacked() {
				if err := dagCache.Evaluate(ctx, *ctrMount.FileSource.Result); err != nil {
					return materialized, fmt.Errorf("evaluate file source for mount %d: %w", i, err)
				}
			}
			mountState.Selector = ctrMount.FileSource.self().File
			ref, err := ctrMount.FileSource.self().getSnapshot()
			if err != nil {
				return materialized, fmt.Errorf("failed to get file snapshot for mount %d: %w", i, err)
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

func execMountWithSession(mountable bkcache.Mountable, g bksession.Group) executor.Mount {
	_, readonly := mountable.(bkcache.ImmutableRef)
	return executor.Mount{
		Src:      &sessionMountable{mountable: mountable, group: g},
		Readonly: readonly,
	}
}

type sessionMountable struct {
	mountable bkcache.Mountable
	group     bksession.Group
}

func (mountable *sessionMountable) Mount(ctx context.Context, readonly bool) (snapshot.Mountable, error) {
	return mountable.mountable.Mount(ctx, readonly, mountable.group)
}

func execTmpFSMountable(cache bkcache.SnapshotManager, opt *pb.TmpfsOpt) bkcache.Mountable {
	return &execTmpFS{
		idmap: cache.IdentityMapping(),
		opt:   opt,
	}
}

type execTmpFS struct {
	idmap *idtools.IdentityMapping
	opt   *pb.TmpfsOpt
}

func (tmpfs *execTmpFS) Mount(_ context.Context, readonly bool, _ bksession.Group) (snapshot.Mountable, error) {
	return &execTmpFSMount{
		readonly: readonly,
		idmap:    tmpfs.idmap,
		opt:      tmpfs.opt,
	}, nil
}

type execTmpFSMount struct {
	readonly bool
	idmap    *idtools.IdentityMapping
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

func (tmpfs *execTmpFSMount) IdentityMapping() *idtools.IdentityMapping {
	return tmpfs.idmap
}

func prepareExecSecretMount(ctx context.Context, cache bkcache.SnapshotManager, cfg *execSecretMountConfig) (bkcache.Mountable, error) {
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
		uid:   cfg.UID,
		gid:   cfg.GID,
		mode:  cfg.Mode,
		data:  data,
		idmap: cache.IdentityMapping(),
	}, nil
}

type execSecretMount struct {
	uid   int
	gid   int
	mode  fs.FileMode
	data  []byte
	idmap *idtools.IdentityMapping
}

func (secret *execSecretMount) Mount(_ context.Context, _ bool, _ bksession.Group) (snapshot.Mountable, error) {
	return &execSecretMountInstance{
		secret: secret,
		idmap:  secret.idmap,
	}, nil
}

type execSecretMountInstance struct {
	secret *execSecretMount
	idmap  *idtools.IdentityMapping
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

	uid := secret.secret.uid
	gid := secret.secret.gid
	if secret.idmap != nil {
		hostIdentity, err := secret.idmap.ToHost(idtools.Identity{
			UID: uid,
			GID: gid,
		})
		if err != nil {
			_ = cleanup()
			return nil, nil, err
		}
		uid = hostIdentity.UID
		gid = hostIdentity.GID
	}

	if err := os.Chown(fp, uid, gid); err != nil {
		_ = cleanup()
		return nil, nil, err
	}
	if err := os.Chmod(fp, os.FileMode(secret.secret.mode&0o777)); err != nil {
		_ = cleanup()
		return nil, nil, err
	}

	return []ctrdmount.Mount{{
		Type:    "bind",
		Source:  fp,
		Options: append([]string{"ro", "rbind", "nodev", "nosuid"}, mountOpts...),
	}}, cleanup, nil
}

func (secret *execSecretMountInstance) IdentityMapping() *idtools.IdentityMapping {
	return secret.idmap
}

func prepareExecSSHMount(ctx context.Context, cache bkcache.SnapshotManager, cfg *execSSHMountConfig) (bkcache.Mountable, error) {
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
		idmap:  cache.IdentityMapping(),
	}, nil
}

type execSSHMount struct {
	socket dagql.ObjectResult[*Socket]
	uid    int
	gid    int
	mode   fs.FileMode
	idmap  *idtools.IdentityMapping
}

func (ssh *execSSHMount) Mount(ctx context.Context, _ bool, _ bksession.Group) (snapshot.Mountable, error) {
	sock, cleanup, err := ssh.socket.Self().MountSSHAgent(ctx)
	if err != nil {
		return nil, err
	}
	return &execSSHMountInstance{
		ssh:     ssh,
		sock:    sock,
		cleanup: cleanup,
		idmap:   ssh.idmap,
	}, nil
}

type execSSHMountInstance struct {
	ssh     *execSSHMount
	sock    string
	cleanup func() error
	idmap   *idtools.IdentityMapping
}

func (ssh *execSSHMountInstance) Mount() ([]ctrdmount.Mount, func() error, error) {
	uid := ssh.ssh.uid
	gid := ssh.ssh.gid
	if ssh.idmap != nil {
		hostIdentity, err := ssh.idmap.ToHost(idtools.Identity{
			UID: uid,
			GID: gid,
		})
		if err != nil {
			return nil, nil, err
		}
		uid = hostIdentity.UID
		gid = hostIdentity.GID
	}

	if err := os.Chown(ssh.sock, uid, gid); err != nil {
		if ssh.cleanup != nil {
			_ = ssh.cleanup()
		}
		return nil, nil, err
	}
	if err := os.Chmod(ssh.sock, os.FileMode(ssh.ssh.mode&0o777)); err != nil {
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

func (ssh *execSSHMountInstance) IdentityMapping() *idtools.IdentityMapping {
	return ssh.idmap
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithExec(
	ctx context.Context,
	parent dagql.ObjectResult[*Container],
	opts ContainerExecOpts,
	execMD *buildkit.ExecutionMetadata,
	extractModuleError bool,
) error {
	state := &ContainerExecState{
		LazyState:          NewLazyState(),
		Parent:             parent,
		Opts:               opts,
		ExecMD:             execMD,
		ExtractModuleError: extractModuleError,
		Container:          container,
	}
	container.Lazy = &ContainerExecLazy{State: state}
	container.ImageRef = ""
	container.MetaSnapshot = nil

	rootfsOutput := &Directory{
		Dir:      "/",
		Platform: container.Platform,
		Services: slices.Clone(container.Services),
		Lazy:     &DirectoryFromContainerLazy{Container: container},
	}
	if container.FS != nil && container.FS.self() != nil {
		rootfsOutput.Dir = container.FS.self().Dir
		rootfsOutput.Platform = container.FS.self().Platform
		rootfsOutput.Services = slices.Clone(container.FS.self().Services)
	}
	container.FS = newContainerDirectoryValueSource(rootfsOutput)

	for i, ctrMount := range container.Mounts {
		if ctrMount.Readonly {
			continue
		}
		switch {
		case ctrMount.DirectorySource != nil:
			if ctrMount.DirectorySource.self() == nil {
				return fmt.Errorf("mount %d has nil directory source", i)
			}
			dirMnt := ctrMount.DirectorySource.self()
			outputDir := &Directory{
				Dir:      dirMnt.Dir,
				Platform: dirMnt.Platform,
				Services: slices.Clone(dirMnt.Services),
				Lazy:     &DirectoryFromContainerLazy{Container: container},
			}
			ctrMount.DirectorySource = newContainerDirectoryValueSource(outputDir)
			container.Mounts[i] = ctrMount
		case ctrMount.FileSource != nil:
			if ctrMount.FileSource.self() == nil {
				return fmt.Errorf("mount %d has nil file source", i)
			}
			fileMnt := ctrMount.FileSource.self()
			outputFile := &File{
				File:     fileMnt.File,
				Platform: fileMnt.Platform,
				Services: slices.Clone(fileMnt.Services),
				Lazy:     &FileFromContainerLazy{Container: container},
			}
			ctrMount.FileSource = newContainerFileValueSource(outputFile)
			container.Mounts[i] = ctrMount
		}
	}
	return nil
}

func (state *ContainerExecState) Evaluate(ctx context.Context) (rerr error) {
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

		parent := state.Parent.Self()
		if parent == nil {
			return fmt.Errorf("exec parent is nil")
		}
		container := state.Container
		if container == nil {
			return fmt.Errorf("exec output container is nil")
		}
		inputRootFS := parent.FS
		inputMounts := slices.Clone(parent.Mounts)

		query, err := CurrentQuery(ctx)
		if err != nil {
			return fmt.Errorf("get current query: %w", err)
		}

		secretEnv, err := container.secretEnvValues(ctx)
		if err != nil {
			return err
		}

		execMD, err := container.execMeta(ctx, state.Opts, state.ExecMD)
		if err != nil {
			return err
		}
		state.ExecMD = execMD

		cache := query.BuildkitCache()

		metaSpec, err := container.metaSpec(ctx, state.Opts)
		if err != nil {
			return err
		}

		bkClient, err := query.Buildkit(ctx)
		if err != nil {
			return fmt.Errorf("failed to get buildkit client: %w", err)
		}
		bkSessionGroup := NewSessionGroup(bkClient.ID())
		opWorker := bkClient.Worker
		causeCtx := trace.SpanContextFromContext(ctx)
		if opWorker == nil {
			return fmt.Errorf("missing buildkit worker")
		}

		rootfsOutput := container.FS.Value
		rootOutputBinding := func(ref bkcache.ImmutableRef) error {
			if rootfsOutput == nil {
				return fmt.Errorf("exec rootfs output is nil")
			}
			return rootfsOutput.setSnapshot(ref)
		}
		metaOutputBinding := func(ref bkcache.ImmutableRef) error {
			container.MetaSnapshot = ref
			return nil
		}
		mountOutputBindings := make([]func(bkcache.ImmutableRef) error, len(container.Mounts))
		for i, ctrMount := range container.Mounts {
			if ctrMount.Readonly {
				continue
			}
			switch {
			case ctrMount.DirectorySource != nil && ctrMount.DirectorySource.Value != nil:
				outputDir := ctrMount.DirectorySource.Value
				mountOutputBindings[i] = func(ref bkcache.ImmutableRef) error {
					return outputDir.setSnapshot(ref)
				}
			case ctrMount.FileSource != nil && ctrMount.FileSource.Value != nil:
				outputFile := ctrMount.FileSource.Value
				mountOutputBindings[i] = func(ref bkcache.ImmutableRef) error {
					return outputFile.setSnapshot(ref)
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
				if state.OutputRef == nil {
					continue
				}
				releaseErr = errors.Join(releaseErr, state.OutputRef.Release(context.WithoutCancel(ctx)))
				state.OutputRef = nil
			}
			return releaseErr
		}
		applyOutputs := func() error {
			for _, state := range mountStates {
				if state.ApplyOutput == nil || state.OutputRef == nil {
					continue
				}
				var out bkcache.ImmutableRef
				if mutable, ok := state.OutputRef.(bkcache.MutableRef); ok {
					committed, err := mutable.Commit(ctx)
					if err != nil {
						return fmt.Errorf("error committing %s: %w", mutable.ID(), err)
					}
					out = committed
				} else {
					out = state.OutputRef.(bkcache.ImmutableRef)
				}
				if err := state.ApplyOutput(out); err != nil {
					return err
				}
				state.OutputRef = nil
			}
			return nil
		}

		makeMutable := func(dest string, ref bkcache.ImmutableRef) (bkcache.MutableRef, error) {
			desc := fmt.Sprintf("mount %s from exec %s", dest, strings.Join(metaSpec.Args, " "))
			return cache.New(ctx, ref, nil, bkcache.WithDescription(desc))
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
						state.OutputRef = iref.Clone()
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
						state.OutputRef = active
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
				mountable = execTmpFSMountable(cache, state.TmpfsOpt)

			case pb.MountType_SECRET:
				mountable, err = prepareExecSecretMount(ctx, cache, state.Secret)
				if err != nil {
					return err
				}

			case pb.MountType_SSH:
				mountable, err = prepareExecSSHMount(ctx, cache, state.SSH)
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
				root := execMountWithSession(mountable, bkSessionGroup)
				root.Selector = state.Selector
				rootMount = root
			} else {
				mount := execMountWithSession(mountable, bkSessionGroup)
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
		if inputRootFS != nil && inputRootFS.self() != nil {
			if inputRootFS.isResultBacked() {
				if err := dagCache.Evaluate(ctx, *inputRootFS.Result); err != nil {
					return failPrepare(fmt.Errorf("evaluate rootfs source: %w", err))
				}
			}
			rootState.Selector = inputRootFS.self().Dir
			if rootState.Selector == "" {
				rootState.Selector = "/"
			}
			ref, err := inputRootFS.self().getSnapshot()
			if err != nil {
				return failPrepare(fmt.Errorf("parent rootfs should already be materialized after parent evaluation: %w", err))
			}
			rootState.SourceRef = ref
		}
		if err := materializeState(rootState); err != nil {
			return failPrepare(err)
		}

		metaState := &execMountState{
			Dest:        buildkit.MetaMountDestPath,
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
				if ctrMount.DirectorySource.self() == nil {
					return failPrepare(fmt.Errorf("mount %d has nil directory source", i))
				}
				if ctrMount.DirectorySource.isResultBacked() {
					if err := dagCache.Evaluate(ctx, *ctrMount.DirectorySource.Result); err != nil {
						return failPrepare(fmt.Errorf("evaluate directory source for mount %d: %w", i, err))
					}
				}
				mountState.Selector = ctrMount.DirectorySource.self().Dir
				ref, err := ctrMount.DirectorySource.self().getSnapshot()
				if err != nil {
					return failPrepare(fmt.Errorf("parent directory mount %d should already be materialized after parent evaluation: %w", i, err))
				}
				mountState.SourceRef = ref

			case ctrMount.FileSource != nil:
				if ctrMount.FileSource.self() == nil {
					return failPrepare(fmt.Errorf("mount %d has nil file source", i))
				}
				if ctrMount.FileSource.isResultBacked() {
					if err := dagCache.Evaluate(ctx, *ctrMount.FileSource.Result); err != nil {
						return failPrepare(fmt.Errorf("evaluate file source for mount %d: %w", i, err))
					}
				}
				mountState.Selector = ctrMount.FileSource.self().File
				ref, err := ctrMount.FileSource.self().getSnapshot()
				if err != nil {
					return failPrepare(fmt.Errorf("parent file mount %d should already be materialized after parent evaluation: %w", i, err))
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
				case state.OutputRef != nil:
					switch out := state.OutputRef.(type) {
					case bkcache.MutableRef:
						iref, err := out.Commit(ctx)
						if err != nil {
							return nil, fmt.Errorf("commit output ref %s: %w", out.ID(), err)
						}
						return iref, nil
					case bkcache.ImmutableRef:
						return out.Clone(), nil
					default:
						return nil, fmt.Errorf("unexpected output ref type %T", state.OutputRef)
					}
				case state.ActiveRef != nil:
					iref, err := state.ActiveRef.Commit(ctx)
					if err != nil {
						return nil, fmt.Errorf("commit active ref %s: %w", state.ActiveRef.ID(), err)
					}
					return iref, nil
				default:
					sourceRef, ok := state.SourceRef.(bkcache.ImmutableRef)
					if ok && sourceRef != nil {
						return sourceRef.Clone(), nil
					}
					return nil, nil
				}
			}

			resolvedRefs := []bkcache.ImmutableRef{}
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
			resolveAndTrackFailureRef := func(state *execMountState) (bkcache.ImmutableRef, error) {
				ref, err := resolveFailureRef(state)
				if err != nil {
					return nil, err
				}
				if ref != nil {
					resolvedRefs = append(resolvedRefs, ref)
				}
				return ref, nil
			}
			for _, mountState := range mountStates {
				keepRef := mountState.Dest == buildkit.MetaMountDestPath
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
				if mountState.Dest == buildkit.MetaMountDestPath {
					metaRef = ref
				}
				if state.ExtractModuleError && mountState.Dest == modMetaDirPath {
					moduleRef = ref
				}
			}

			if state.ExtractModuleError && moduleRef != nil {
				errID, ok, err := moduleErrorIDFromRef(ctx, bkClient, moduleRef)
				if err != nil {
					rerr = errors.Join(rerr, fmt.Errorf("extract module error: %w", err))
					return
				}
				if ok {
					rerr = &ModuleExecError{
						Err:     rerr,
						ErrorID: errID,
					}
					return
				}
			}

			execMDPresent := execMD != nil
			execInternal := false
			execID := ""
			hasMetaSpec := metaSpec != nil
			registerAllowed := false
			if execMDPresent {
				execInternal = execMD.Internal
				execID = execMD.ExecID
			}
			if bkClient.Interactive && execMDPresent && !execInternal && hasMetaSpec {
				if execID == "" {
					registerAllowed = true
				} else {
					registerAllowed = bkClient.RegisterInteractiveExec(execID)
				}
			}
			if bkClient.Interactive &&
				execMDPresent &&
				!execInternal &&
				hasMetaSpec &&
				registerAllowed {
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
					callDig = callID.ContentPreferredDigest()
				}
				meta := *metaSpec
					meta.Args = []string{"/bin/sh"}
					if len(bkClient.InteractiveCommand) > 0 {
						meta.Args = bkClient.InteractiveCommand
					}
					terminalContainer := cloneContainerForTerminal(container)
					terminalContainer.FS = cloneTerminalDirectorySource(inputRootFS)
					terminalContainer.Mounts = cloneTerminalMounts(inputMounts)
					terminalContainer.MetaSnapshot = metaRef
				if len(mountStates) >= 1 {
					rootRef, err := resolveAndTrackFailureRef(mountStates[0])
					if err != nil {
						rerr = fmt.Errorf("resolve failed rootfs for terminal: %w", err)
						return
					}
					if rootRef != nil {
						rootDir := &Directory{
							Dir:      "/",
							Platform: terminalContainer.Platform,
							Services: terminalContainer.Services,
						}
						if inputRootFS != nil && inputRootFS.self() != nil {
							rootDir.Dir = inputRootFS.self().Dir
							rootDir.Platform = inputRootFS.self().Platform
							rootDir.Services = inputRootFS.self().Services
						}
						if err := rootDir.setSnapshot(rootRef); err != nil {
							rerr = fmt.Errorf("rebuild failed rootfs for terminal: %w", err)
							return
						}
						terminalContainer.setBareRootFS(rootDir)
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
					case ctrMount.DirectorySource != nil && ctrMount.DirectorySource.self() != nil && !ctrMount.Readonly:
						outputDir := &Directory{
							Dir:      ctrMount.DirectorySource.self().Dir,
							Platform: ctrMount.DirectorySource.self().Platform,
							Services: ctrMount.DirectorySource.self().Services,
						}
						if err := outputDir.setSnapshot(mountRef); err != nil {
							rerr = fmt.Errorf("rebuild failed directory mount %d for terminal: %w", i, err)
							return
						}
						ctrMount.DirectorySource = newContainerDirectoryValueSource(outputDir)
						terminalContainer.Mounts[i] = ctrMount
					case ctrMount.FileSource != nil && ctrMount.FileSource.self() != nil && !ctrMount.Readonly:
						outputFile := &File{
							File:     ctrMount.FileSource.self().File,
							Platform: ctrMount.FileSource.self().Platform,
							Services: ctrMount.FileSource.self().Services,
						}
						if err := outputFile.setSnapshot(mountRef); err != nil {
							rerr = fmt.Errorf("rebuild failed file mount %d for terminal: %w", i, err)
							return
						}
						ctrMount.FileSource = newContainerFileValueSource(outputFile)
						terminalContainer.Mounts[i] = ctrMount
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
			}

			var existingExecErr *ExecError
			if errors.As(rerr, &existingExecErr) {
				return
			}

			execErr, ok, err := execErrorFromMetaRef(ctx, bkClient, causeCtx, rerr, metaSpec, metaRef)
			if err != nil {
				rerr = errors.Join(err, rerr)
				return
			}
			if ok {
				rerr = execErr
			}
		}()

		emu, err := getEmulator(ctx, specs.Platform(container.Platform))
		if err != nil {
			return err
		}
		if emu != nil {
			metaSpec.Args = append([]string{buildkit.BuildkitQemuEmulatorMountPoint}, metaSpec.Args...)
			execMounts = append(execMounts, executor.Mount{
				Readonly: true,
				Src:      emu,
				Dest:     buildkit.BuildkitQemuEmulatorMountPoint,
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
		if state.Opts.Stdin != "" {
			procInfo.Stdin = io.NopCloser(strings.NewReader(state.Opts.Stdin))
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
		Container:          container,
	}
	container.Lazy = &ContainerExecLazy{State: state}
	container.ImageRef = ""
	container.MetaSnapshot = nil
	if container.FS != nil && container.FS.Value != nil && decodedRootFS.Kind == persistedContainerValueFormOutputPending {
		container.FS.Value.Lazy = &DirectoryFromContainerLazy{Container: container}
	}
	for i, decodedMount := range decodedMounts {
		if container.Mounts[i].Readonly || decodedMount.Kind != persistedContainerValueFormOutputPending {
			continue
		}
		switch {
		case container.Mounts[i].DirectorySource != nil && container.Mounts[i].DirectorySource.Value != nil:
			container.Mounts[i].DirectorySource.Value.Lazy = &DirectoryFromContainerLazy{Container: container}
		case container.Mounts[i].FileSource != nil && container.Mounts[i].FileSource.Value != nil:
			container.Mounts[i].FileSource.Value.Lazy = &FileFromContainerLazy{Container: container}
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
	return container.metaFileContents(ctx, buildkit.MetaMountStdoutPath)
}

func (container *Container) Stderr(ctx context.Context) (string, error) {
	return container.metaFileContents(ctx, buildkit.MetaMountStderrPath)
}

func (container *Container) CombinedOutput(ctx context.Context) (string, error) {
	return container.metaFileContents(ctx, buildkit.MetaMountCombinedOutputPath)
}

func (container *Container) ExitCode(ctx context.Context) (int, error) {
	contents, err := container.metaFileContents(ctx, buildkit.MetaMountExitCodePath)
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

	file, err := NewFileWithSnapshot(filePath, container.Platform, container.Services, container.MetaSnapshot)
	if err != nil {
		return "", err
	}

	content, err := file.Contents(ctx, nil, nil)
	if err != nil {
		if errors.Is(err, errEmptyResultRef) {
			return "", ErrNoCommand
		}
		return "", err
	}
	return string(content), nil
}
