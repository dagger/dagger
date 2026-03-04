package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	ctrdmount "github.com/containerd/containerd/v2/core/mount"
	"golang.org/x/sync/errgroup"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/executor"
	"github.com/dagger/dagger/internal/buildkit/identity"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/session/secrets"
	"github.com/dagger/dagger/internal/buildkit/session/sshforward"
	"github.com/dagger/dagger/internal/buildkit/snapshot"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/util/grpcerrors"
	utilsystem "github.com/dagger/dagger/internal/buildkit/util/system"
	"github.com/docker/docker/pkg/idtools"
	"github.com/moby/sys/userns"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"google.golang.org/grpc/codes"

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

func (container *Container) execMeta(ctx context.Context, opts ContainerExecOpts, parent *buildkit.ExecutionMetadata) (*buildkit.ExecutionMetadata, error) {
	execMD := buildkit.ExecutionMetadata{}
	if parent != nil {
		execMD = *parent
	}
	preAliasKeys := slices.Collect(maps.Keys(execMD.HostAliases))
	slices.Sort(preAliasKeys)
	serviceHosts := make([]string, 0, len(container.Services))
	for _, bnd := range container.Services {
		serviceHosts = append(serviceHosts, bnd.Hostname)
	}
	slices.Sort(serviceHosts)
	slog.Info("execMeta init",
		"parentSet", parent != nil,
		"preHostAliases", preAliasKeys,
		"serviceHosts", serviceHosts,
		"args", opts.Args,
	)

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

	if execMD.CallID == nil {
		execMD.CallID = dagql.CurrentID(ctx)
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

	var callerModID *call.ID
	if execMD.EncodedContentModuleID != "" {
		callerModID = new(call.ID)
		if err := callerModID.Decode(execMD.EncodedContentModuleID); err != nil {
			return nil, fmt.Errorf("failed to decode content-scoped module ID: %w", err)
		}
	} else if execMD.EncodedModuleID != "" {
		callerModID = new(call.ID)
		if err := callerModID.Decode(execMD.EncodedModuleID); err != nil {
			return nil, fmt.Errorf("failed to decode module ID: %w", err)
		}
	} else if callerMod, err := query.CurrentModule(ctx); err == nil && callerMod != nil {
		callerModID, err = callerMod.SourceContentScopedID(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get caller module content-scoped ID: %w", err)
		}
	}

	if callerModID != nil {
		// allow the exec to reach services scoped to the module that installed it
		execMD.ExtraSearchDomains = append(execMD.ExtraSearchDomains, network.ModuleDomain(callerModID, clientMetadata.SessionID))
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
	postAliasKeys := slices.Collect(maps.Keys(execMD.HostAliases))
	slices.Sort(postAliasKeys)
	slog.Info("execMeta host aliases",
		"hostAliases", postAliasKeys,
		"args", opts.Args,
	)

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

func (container *Container) secretEnvs() (secretEnvs []*pb.SecretEnv) {
	for _, secret := range container.Secrets {
		if secret.EnvName != "" {
			secretEnvs = append(secretEnvs, &pb.SecretEnv{
				ID:   SecretIDDigest(secret.Secret.ID()).String(),
				Name: secret.EnvName,
			})
		}
	}
	return secretEnvs
}

type execMountState struct {
	Dest      string
	Selector  string
	Readonly  bool
	MountType pb.MountType

	SourceRef bkcache.Ref

	TmpfsOpt  *pb.TmpfsOpt
	SecretOpt *pb.SecretOpt
	SSHOpt    *pb.SSHOpt

	ApplyOutput func(bkcache.ImmutableRef)

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
	rootOutput func(bkcache.ImmutableRef),
	metaOutput func(bkcache.ImmutableRef),
	mountOutputs []func(bkcache.ImmutableRef),
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
			mountable, err = prepareExecSecretMount(ctx, cache, session, state.SecretOpt, g)
			if err != nil {
				return err
			}

		case pb.MountType_SSH:
			mountable, err = prepareExecSSHMount(ctx, cache, session, state.SSHOpt, g)
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

	rootState := &execMountState{
		Dest:        pb.RootMount,
		Selector:    "/",
		MountType:   pb.MountType_BIND,
		ApplyOutput: rootOutput,
	}
	if container.FS != nil && container.FS.Self() != nil {
		rootState.Selector = container.FS.Self().Dir
		if rootState.Selector == "" {
			rootState.Selector = "/"
		}
		ref, err := container.FS.Self().getSnapshot(ctx)
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
	if container.Meta != nil {
		ref, err := container.Meta.getSnapshot(ctx)
		if err != nil {
			return materialized, fmt.Errorf("failed to get meta snapshot: %w", err)
		}
		metaState.SourceRef = ref
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
			if ctrMount.DirectorySource.Self() == nil {
				return materialized, fmt.Errorf("mount %d has nil directory source", i)
			}
			mountState.Selector = ctrMount.DirectorySource.Self().Dir
			ref, err := ctrMount.DirectorySource.Self().getSnapshot(ctx)
			if err != nil {
				return materialized, fmt.Errorf("failed to get directory snapshot for mount %d: %w", i, err)
			}
			mountState.SourceRef = ref

		case ctrMount.FileSource != nil:
			if ctrMount.FileSource.Self() == nil {
				return materialized, fmt.Errorf("mount %d has nil file source", i)
			}
			mountState.Selector = ctrMount.FileSource.Self().File
			ref, err := ctrMount.FileSource.Self().getSnapshot(ctx)
			if err != nil {
				return materialized, fmt.Errorf("failed to get file snapshot for mount %d: %w", i, err)
			}
			mountState.SourceRef = ref

		case ctrMount.CacheSource != nil:
			cacheSrc := ctrMount.CacheSource
			if cacheSrc.Volume.Self() == nil {
				return materialized, fmt.Errorf("mount %d has nil cache volume source", i)
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
			SecretOpt: &pb.SecretOpt{
				ID:   SecretIDDigest(secret.Secret.ID()).String(),
				Uid:  uint32(uid),
				Gid:  uint32(gid),
				Mode: uint32(secret.Mode),
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
			SSHOpt: &pb.SSHOpt{
				ID:   socket.Source.LLBID(),
				Uid:  uint32(uid),
				Gid:  uint32(gid),
				Mode: 0o600,
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

func prepareExecSecretMount(ctx context.Context, cache bkcache.SnapshotManager, session *bksession.Manager, secretOpt *pb.SecretOpt, g bksession.Group) (bkcache.Mountable, error) {
	if secretOpt == nil {
		return nil, fmt.Errorf("invalid secret mount options")
	}
	secret := *secretOpt
	if secret.ID == "" {
		return nil, fmt.Errorf("secret ID missing from mount options")
	}

	var (
		data []byte
		err  error
	)
	err = session.Any(ctx, g, func(ctx context.Context, _ string, caller bksession.Caller) error {
		data, err = secrets.GetSecret(ctx, caller, secret.ID)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) && secret.Optional {
			return nil, nil
		}
		return nil, err
	}

	return &execSecretMount{
		secret: &secret,
		data:   data,
		idmap:  cache.IdentityMapping(),
	}, nil
}

type execSecretMount struct {
	secret *pb.SecretOpt
	data   []byte
	idmap  *idtools.IdentityMapping
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
	if secret.secret.secret.Mode&0o111 == 0 {
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

	uid := int(secret.secret.secret.Uid)
	gid := int(secret.secret.secret.Gid)
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
	if err := os.Chmod(fp, os.FileMode(secret.secret.secret.Mode&0o777)); err != nil {
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

func prepareExecSSHMount(ctx context.Context, cache bkcache.SnapshotManager, session *bksession.Manager, sshOpt *pb.SSHOpt, g bksession.Group) (bkcache.Mountable, error) {
	if sshOpt == nil {
		return nil, fmt.Errorf("invalid ssh mount options")
	}
	ssh := *sshOpt

	var caller bksession.Caller
	err := session.Any(ctx, g, func(ctx context.Context, _ string, c bksession.Caller) error {
		if err := sshforward.CheckSSHID(ctx, c, ssh.ID); err != nil {
			if ssh.Optional {
				return nil
			}
			if grpcerrors.Code(err) == codes.Unimplemented {
				return fmt.Errorf("no SSH key %q forwarded from the client", ssh.ID)
			}
			return err
		}
		caller = c
		return nil
	})
	if err != nil {
		return nil, err
	}
	if caller == nil {
		return nil, nil
	}

	return &execSSHMount{
		sshOpt: &ssh,
		caller: caller,
		idmap:  cache.IdentityMapping(),
	}, nil
}

type execSSHMount struct {
	sshOpt *pb.SSHOpt
	caller bksession.Caller
	idmap  *idtools.IdentityMapping
}

func (ssh *execSSHMount) Mount(_ context.Context, _ bool, _ bksession.Group) (snapshot.Mountable, error) {
	return &execSSHMountInstance{
		ssh:   ssh,
		idmap: ssh.idmap,
	}, nil
}

type execSSHMountInstance struct {
	ssh   *execSSHMount
	idmap *idtools.IdentityMapping
}

func (ssh *execSSHMountInstance) Mount() ([]ctrdmount.Mount, func() error, error) {
	ctx, cancel := context.WithCancelCause(context.TODO())

	uid := int(ssh.ssh.sshOpt.Uid)
	gid := int(ssh.ssh.sshOpt.Gid)
	if ssh.idmap != nil {
		hostIdentity, err := ssh.idmap.ToHost(idtools.Identity{
			UID: uid,
			GID: gid,
		})
		if err != nil {
			cancel(err)
			return nil, nil, err
		}
		uid = hostIdentity.UID
		gid = hostIdentity.GID
	}

	sock, cleanup, err := sshforward.MountSSHSocket(ctx, ssh.ssh.caller, sshforward.SocketOpt{
		ID:   ssh.ssh.sshOpt.ID,
		UID:  uid,
		GID:  gid,
		Mode: int(ssh.ssh.sshOpt.Mode & 0o777),
	})
	if err != nil {
		cancel(err)
		return nil, nil, err
	}
	release := func() error {
		var err error
		if cleanup != nil {
			err = cleanup()
		}
		cancel(err)
		return err
	}

	return []ctrdmount.Mount{{
		Type:    "bind",
		Source:  sock,
		Options: []string{"rbind"},
	}}, release, nil
}

func (ssh *execSSHMountInstance) IdentityMapping() *idtools.IdentityMapping {
	return ssh.idmap
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithExec(
	ctx context.Context,
	opts ContainerExecOpts,
	execMD *buildkit.ExecutionMetadata,
	extractModuleError bool,
) error {
	gate := NewLazyState()
	gateRun := func(ctx context.Context) error {
		return gate.Evaluate(ctx, "container exec")
	}

	inputRootFS := container.FS
	inputMounts := slices.Clone(container.Mounts)
	// withExec mutates container filesystem state, so imageRef is no longer valid.
	container.ImageRef = ""

	rootfsOutput := &Directory{
		Dir:       "/",
		Platform:  container.Platform,
		Services:  container.Services,
		LazyState: NewLazyState(),
	}
	rootfsOutput.LazyInit = gateRun
	if inputRootFS != nil && inputRootFS.Self() != nil {
		rootfsOutput.Dir = inputRootFS.Self().Dir
		rootfsOutput.Platform = inputRootFS.Self().Platform
		rootfsOutput.Services = inputRootFS.Self().Services
	}
	updatedRootFS, err := UpdatedRootFS(ctx, container, rootfsOutput)
	if err != nil {
		return fmt.Errorf("failed to initialize rootfs output: %w", err)
	}
	container.FS = updatedRootFS

	metaOutput := &Directory{
		Dir:       "/",
		Platform:  container.Platform,
		Services:  container.Services,
		LazyState: NewLazyState(),
	}
	metaOutput.LazyInit = gateRun
	container.Meta = metaOutput
	container.MetaResult = nil
	if container.OpID != nil {
		metaResultID := container.OpID.Append(metaOutput.Type(), "__daggerMetaOutput")
		srv, err := CurrentDagqlServer(ctx)
		if err != nil {
			return fmt.Errorf("failed to get dagql server for meta output result: %w", err)
		}
		metaOutputResult, err := dagql.NewObjectResultForID(metaOutput, srv, metaResultID)
		if err != nil {
			return fmt.Errorf("failed to build meta output result: %w", err)
		}
		container.MetaResult = &metaOutputResult
	}

	rootOutputBinding := func(ref bkcache.ImmutableRef) {
		rootfsOutput.setSnapshot(ref)
	}
	metaOutputBinding := func(ref bkcache.ImmutableRef) {
		metaOutput.setSnapshot(ref)
	}
	mountOutputBindings := make([]func(bkcache.ImmutableRef), len(container.Mounts))

	for i, ctrMount := range container.Mounts {
		if ctrMount.Readonly {
			continue
		}

		switch {
		case ctrMount.DirectorySource != nil:
			if ctrMount.DirectorySource.Self() == nil {
				return fmt.Errorf("mount %d has nil directory source", i)
			}
			dirMnt := ctrMount.DirectorySource.Self()
			outputDir := &Directory{
				Dir:       dirMnt.Dir,
				Platform:  dirMnt.Platform,
				Services:  dirMnt.Services,
				LazyState: NewLazyState(),
			}
			outputDir.LazyInit = gateRun
			updatedMnt, err := updatedDirMount(ctx, container, outputDir, ctrMount.Target)
			if err != nil {
				return fmt.Errorf("failed to initialize directory mount output %d: %w", i, err)
			}
			ctrMount.DirectorySource = updatedMnt
			container.Mounts[i] = ctrMount
			dirOutput := outputDir
			mountOutputBindings[i] = func(ref bkcache.ImmutableRef) {
				dirOutput.setSnapshot(ref)
			}

		case ctrMount.FileSource != nil:
			if ctrMount.FileSource.Self() == nil {
				return fmt.Errorf("mount %d has nil file source", i)
			}
			fileMnt := ctrMount.FileSource.Self()
			outputFile := &File{
				File:      fileMnt.File,
				Platform:  fileMnt.Platform,
				Services:  fileMnt.Services,
				LazyState: NewLazyState(),
			}
			outputFile.LazyInit = gateRun
			updatedMnt, err := updatedFileMount(ctx, container, outputFile, ctrMount.Target)
			if err != nil {
				return fmt.Errorf("failed to initialize file mount output %d: %w", i, err)
			}
			ctrMount.FileSource = updatedMnt
			container.Mounts[i] = ctrMount
			fileOutput := outputFile
			mountOutputBindings[i] = func(ref bkcache.ImmutableRef) {
				fileOutput.setSnapshot(ref)
			}
		}
	}

	gate.LazyInit = func(ctx context.Context) (rerr error) {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return fmt.Errorf("get current query: %w", err)
		}

		secretEnvs := container.secretEnvs()

		execMD, err = container.execMeta(ctx, opts, execMD)
		if err != nil {
			return err
		}

		cache := query.BuildkitCache()
		session := query.BuildkitSession()

		metaSpec, err := container.metaSpec(ctx, opts)
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
				state.ApplyOutput(out)
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
				mountable, err = prepareExecSecretMount(ctx, cache, session, state.SecretOpt, bkSessionGroup)
				if err != nil {
					return err
				}

			case pb.MountType_SSH:
				mountable, err = prepareExecSSHMount(ctx, cache, session, state.SSHOpt, bkSessionGroup)
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
		if inputRootFS != nil && inputRootFS.Self() != nil {
			rootState.Selector = inputRootFS.Self().Dir
			if rootState.Selector == "" {
				rootState.Selector = "/"
			}
			ref, err := inputRootFS.Self().getSnapshot(ctx)
			if err != nil {
				return failPrepare(fmt.Errorf("failed to get rootfs snapshot: %w", err))
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
				if ctrMount.DirectorySource.Self() == nil {
					return failPrepare(fmt.Errorf("mount %d has nil directory source", i))
				}
				mountState.Selector = ctrMount.DirectorySource.Self().Dir
				ref, err := ctrMount.DirectorySource.Self().getSnapshot(ctx)
				if err != nil {
					return failPrepare(fmt.Errorf("failed to get directory snapshot for mount %d: %w", i, err))
				}
				mountState.SourceRef = ref

			case ctrMount.FileSource != nil:
				if ctrMount.FileSource.Self() == nil {
					return failPrepare(fmt.Errorf("mount %d has nil file source", i))
				}
				mountState.Selector = ctrMount.FileSource.Self().File
				ref, err := ctrMount.FileSource.Self().getSnapshot(ctx)
				if err != nil {
					return failPrepare(fmt.Errorf("failed to get file snapshot for mount %d: %w", i, err))
				}
				mountState.SourceRef = ref

			case ctrMount.CacheSource != nil:
				cacheSrc := ctrMount.CacheSource
				if cacheSrc.Volume.Self() == nil {
					return failPrepare(fmt.Errorf("mount %d has nil cache volume source", i))
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
				SecretOpt: &pb.SecretOpt{
					ID:   SecretIDDigest(secret.Secret.ID()).String(),
					Uid:  uint32(uid),
					Gid:  uint32(gid),
					Mode: uint32(secret.Mode),
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
				SSHOpt: &pb.SSHOpt{
					ID:   socket.Source.LLBID(),
					Uid:  uint32(uid),
					Gid:  uint32(gid),
					Mode: 0o600,
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
			for _, state := range mountStates {
				keepRef := state.Dest == buildkit.MetaMountDestPath
				if extractModuleError && state.Dest == modMetaDirPath {
					keepRef = true
				}
				if !keepRef {
					continue
				}
				ref, err := resolveFailureRef(state)
				if err != nil {
					rerr = errors.Join(rerr, err)
					continue
				}
				if ref == nil {
					continue
				}
				resolvedRefs = append(resolvedRefs, ref)
				if state.Dest == buildkit.MetaMountDestPath {
					metaRef = ref
				}
				if extractModuleError && state.Dest == modMetaDirPath {
					moduleRef = ref
				}
			}

			if extractModuleError && moduleRef != nil {
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

			if bkClient.Interactive &&
				execMD != nil &&
				!execMD.Internal &&
				metaSpec != nil &&
				(execMD.ExecID == "" || bkClient.RegisterInteractiveExec(execMD.ExecID)) {
				meta := *metaSpec
				meta.Args = []string{"/bin/sh"}
				if len(bkClient.InteractiveCommand) > 0 {
					meta.Args = bkClient.InteractiveCommand
				}
				if err := container.TerminalExecError(ctx, execMD.CallID, execMD, &meta, rerr); err != nil {
					rerr = err
					return
				}
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
		secretEnv, err := loadSecretEnv(ctx, bkSessionGroup, session, secretEnvs)
		if err != nil {
			return err
		}
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
			// Stdin/Stdout/Stderr can be setup in Worker.setupStdio
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

		return nil
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

	if container.Meta == nil {
		return "", ErrNoCommand
	}

	metaSnapshot, err := container.Meta.getSnapshot(ctx)
	if err != nil {
		if errors.Is(err, errEmptyResultRef) {
			return "", ErrNoCommand
		}
		return "", err
	}
	file := &File{
		File:     filePath,
		Platform: container.Platform,
		Services: container.Services,
		Snapshot: metaSnapshot,
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

func loadSecretEnv(ctx context.Context, g bksession.Group, sm *bksession.Manager, secretenv []*pb.SecretEnv) ([]string, error) {
	if len(secretenv) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(secretenv))
	eg, gctx := errgroup.WithContext(ctx)
	var mu sync.Mutex
	for _, sopt := range secretenv {
		id := sopt.ID
		eg.Go(func() error {
			if id == "" {
				return fmt.Errorf("secret ID missing for %q environment variable", sopt.Name)
			}
			var dt []byte
			var err error
			err = sm.Any(gctx, g, func(ctx context.Context, _ string, caller bksession.Caller) error {
				dt, err = secrets.GetSecret(ctx, caller, id)
				if err != nil {
					if errors.Is(err, secrets.ErrNotFound) && sopt.Optional {
						return nil
					}
					return err
				}
				return nil
			})
			if err != nil {
				return err
			}
			mu.Lock()
			out = append(out, fmt.Sprintf("%s=%s", sopt.Name, string(dt)))
			mu.Unlock()
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}
