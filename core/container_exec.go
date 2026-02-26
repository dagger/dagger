package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	"github.com/dagger/dagger/internal/buildkit/executor"
	bkcontainer "github.com/dagger/dagger/internal/buildkit/frontend/gateway/container"
	"github.com/dagger/dagger/internal/buildkit/identity"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/session/secrets"
	bksolver "github.com/dagger/dagger/internal/buildkit/solver"
	"github.com/dagger/dagger/internal/buildkit/solver/llbsolver/errdefs"
	bkmounts "github.com/dagger/dagger/internal/buildkit/solver/llbsolver/mounts"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	utilsystem "github.com/dagger/dagger/internal/buildkit/util/system"
	"github.com/dagger/dagger/internal/buildkit/worker"
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

	metaSpec.Env = addDefaultEnvvar(metaSpec.Env, "PATH", utilsystem.DefaultPathEnv(platform.OS))

	if opts.Expect != ReturnSuccess {
		metaSpec.ValidExitCodes = opts.Expect.ReturnCodes()
	}

	return &metaSpec, nil
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

type ContainerMountData struct {
	Inputs []bkcache.ImmutableRef
	Mounts []*pb.Mount

	// One entry per mount index. Used for exec error context.
	MountRefs []bkcache.ImmutableRef
}

func (container *Container) GetMountData(ctx context.Context) (ContainerMountData, error) {
	var data ContainerMountData

	outputIdx := 0
	inputIdxByRefID := map[string]pb.InputIndex{}

	addMount := func(mount *pb.Mount, ref bkcache.ImmutableRef) {
		if ref != nil {
			if idx, ok := inputIdxByRefID[ref.ID()]; ok {
				mount.Input = idx
			} else {
				mount.Input = pb.InputIndex(len(data.Inputs))
				inputIdxByRefID[ref.ID()] = mount.Input
				data.Inputs = append(data.Inputs, ref)
			}
		} else {
			mount.Input = pb.Empty
		}

		data.Mounts = append(data.Mounts, mount)
		data.MountRefs = append(data.MountRefs, ref)
		if mount.Output != pb.SkipOutput {
			outputIdx++
		}
	}

	rootfsMount := &pb.Mount{
		Dest:         pb.RootMount,
		Selector:     "/",
		Output:       pb.OutputIndex(outputIdx),
		ContentCache: pb.MountContentCache_DEFAULT,
	}
	var rootfsRef bkcache.ImmutableRef
	if container.FS != nil && container.FS.Self() != nil {
		rootfsMount.Selector = container.FS.Self().Dir
		if rootfsMount.Selector == "" {
			rootfsMount.Selector = "/"
		}
		var err error
		rootfsRef, err = container.FS.Self().getSnapshot(ctx)
		if err != nil {
			return data, fmt.Errorf("failed to get rootfs snapshot: %w", err)
		}
	}
	addMount(rootfsMount, rootfsRef)

	metaMount := &pb.Mount{
		Dest:         buildkit.MetaMountDestPath,
		Selector:     "/",
		Output:       pb.OutputIndex(outputIdx),
		ContentCache: pb.MountContentCache_DEFAULT,
	}
	var metaRef bkcache.ImmutableRef
	if container.Meta != nil {
		var err error
		metaRef, err = container.Meta.getSnapshot(ctx)
		if err != nil {
			return data, fmt.Errorf("failed to get meta snapshot: %w", err)
		}
	}
	addMount(metaMount, metaRef)

	for i, ctrMount := range container.Mounts {
		mount := &pb.Mount{
			Dest:         ctrMount.Target,
			Output:       pb.OutputIndex(outputIdx),
			ContentCache: pb.MountContentCache_DEFAULT,
		}
		var mountRef bkcache.ImmutableRef

		switch {
		case ctrMount.DirectorySource != nil:
			if ctrMount.DirectorySource.Self() == nil {
				return data, fmt.Errorf("mount %d has nil directory source", i)
			}
			mount.Selector = ctrMount.DirectorySource.Self().Dir
			var err error
			mountRef, err = ctrMount.DirectorySource.Self().getSnapshot(ctx)
			if err != nil {
				return data, fmt.Errorf("failed to get directory snapshot for mount %d: %w", i, err)
			}

		case ctrMount.FileSource != nil:
			if ctrMount.FileSource.Self() == nil {
				return data, fmt.Errorf("mount %d has nil file source", i)
			}
			mount.Selector = ctrMount.FileSource.Self().File
			var err error
			mountRef, err = ctrMount.FileSource.Self().getSnapshot(ctx)
			if err != nil {
				return data, fmt.Errorf("failed to get file snapshot for mount %d: %w", i, err)
			}

		case ctrMount.CacheSource != nil:
			mount.Output = pb.SkipOutput
			mount.MountType = pb.MountType_CACHE
			mount.CacheOpt = &pb.CacheOpt{
				ID: ctrMount.CacheSource.ID,
			}
			switch ctrMount.CacheSource.SharingMode {
			case CacheSharingModeShared:
				mount.CacheOpt.Sharing = pb.CacheSharingOpt_SHARED
			case CacheSharingModePrivate:
				mount.CacheOpt.Sharing = pb.CacheSharingOpt_PRIVATE
			case CacheSharingModeLocked:
				mount.CacheOpt.Sharing = pb.CacheSharingOpt_LOCKED
			default:
				return data, fmt.Errorf("mount %d has unknown cache sharing mode %q", i, ctrMount.CacheSource.SharingMode)
			}
			if ctrMount.CacheSource.Base != nil && ctrMount.CacheSource.Base.Self() != nil {
				mount.Selector = ctrMount.CacheSource.Base.Self().Dir
				var err error
				mountRef, err = ctrMount.CacheSource.Base.Self().getSnapshot(ctx)
				if err != nil {
					return data, fmt.Errorf("failed to get cache base snapshot for mount %d: %w", i, err)
				}
			}

		case ctrMount.TmpfsSource != nil:
			mount.Output = pb.SkipOutput
			mount.MountType = pb.MountType_TMPFS
			mount.TmpfsOpt = &pb.TmpfsOpt{
				Size_: int64(ctrMount.TmpfsSource.Size),
			}

		default:
			return data, fmt.Errorf("mount %d has no source", i)
		}

		if ctrMount.Readonly {
			mount.Output = pb.SkipOutput
			mount.Readonly = true
		}

		addMount(mount, mountRef)
	}

	for _, secret := range container.Secrets {
		if secret.MountPath == "" {
			continue
		}
		uid, gid := 0, 0
		if secret.Owner != nil {
			uid, gid = secret.Owner.UID, secret.Owner.GID
		}
		addMount(&pb.Mount{
			Dest:      secret.MountPath,
			MountType: pb.MountType_SECRET,
			Output:    pb.SkipOutput,
			SecretOpt: &pb.SecretOpt{
				ID:   SecretIDDigest(secret.Secret.ID()).String(),
				Uid:  uint32(uid),
				Gid:  uint32(gid),
				Mode: uint32(secret.Mode),
			},
		}, nil)
	}

	for i, socket := range container.Sockets {
		if socket.ContainerPath == "" {
			return data, fmt.Errorf("unsupported socket %d: only unix paths are implemented", i)
		}
		uid, gid := 0, 0
		if socket.Owner != nil {
			uid, gid = socket.Owner.UID, socket.Owner.GID
		}
		addMount(&pb.Mount{
			Dest:      socket.ContainerPath,
			MountType: pb.MountType_SSH,
			Output:    pb.SkipOutput,
			SSHOpt: &pb.SSHOpt{
				ID:   socket.Source.LLBID(),
				Uid:  uint32(uid),
				Gid:  uint32(gid),
				Mode: 0o600,
			},
		}, nil)
	}

	return data, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithExec(
	ctx context.Context,
	opts ContainerExecOpts,
	execMD *buildkit.ExecutionMetadata,
) (_ *Container, rerr error) {
	container.Meta = nil
	container.LazyMu = new(sync.Mutex)
	if container.OpID == nil {
		container.OpID = dagql.CurrentID(ctx)
	}
	container.LazyInitComplete = false
	if container.OpID == nil {
		return nil, fmt.Errorf("missing operation ID for withExec")
	}

	container.LazyInit = func(ctx context.Context) error {
		_, err := container.withExecNow(ctx, opts, execMD)
		return err
	}

	return container, nil
}

//nolint:gocyclo
func (container *Container) withExecNow(
	ctx context.Context,
	opts ContainerExecOpts,
	execMD *buildkit.ExecutionMetadata,
) (_ *Container, rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current query: %w", err)
	}

	platform := container.Platform
	if platform.OS == "" {
		platform = query.Platform()
	}

	secretEnvs := container.secretEnvs()

	execMD, err = container.execMeta(ctx, opts, execMD)
	if err != nil {
		return nil, err
	}

	mountData, err := container.GetMountData(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get mount data: %w", err)
	}

	workerRefs := make([]*worker.WorkerRef, 0, len(mountData.Inputs))
	for _, ref := range mountData.Inputs {
		workerRefs = append(workerRefs, &worker.WorkerRef{ImmutableRef: ref})
	}

	cache := query.BuildkitCache()
	session := query.BuildkitSession()

	metaSpec, err := container.metaSpec(ctx, opts)
	if err != nil {
		return nil, err
	}

	bkSessionGroup, _ := buildkit.CurrentBuildkitSessionGroup(ctx)

	bkClient, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}
	opWorker := bkClient.Worker
	causeCtx := trace.SpanContextFromContext(ctx)
	if opWorker == nil {
		return nil, fmt.Errorf("missing buildkit worker")
	}

	mm := bkmounts.NewMountManager(fmt.Sprintf("exec %s", strings.Join(metaSpec.Args, " ")), cache, session)
	p, err := bkcontainer.PrepareMounts(ctx, mm, cache, bkSessionGroup, container.Config.WorkingDir, mountData.Mounts, workerRefs, func(m *pb.Mount, ref bkcache.ImmutableRef) (bkcache.MutableRef, error) {
		desc := fmt.Sprintf("mount %s from exec %s", m.Dest, strings.Join(metaSpec.Args, " "))
		return cache.New(ctx, ref, bkSessionGroup, bkcache.WithDescription(desc))
	}, runtime.GOOS)
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil {
			execInputs := make([]bksolver.Result, len(mountData.Mounts))
			for i, m := range mountData.Mounts {
				if m.Input == pb.Empty {
					continue
				}
				if m.Input < 0 || int(m.Input) >= len(mountData.Inputs) {
					rerr = errors.Join(rerr, fmt.Errorf("mount index %d references input %d outside available inputs (%d)", i, m.Input, len(mountData.Inputs)))
					continue
				}
				if mountData.Inputs[m.Input] == nil {
					continue
				}
				execInputs[i] = worker.NewWorkerRefResult(mountData.Inputs[m.Input].Clone(), opWorker)
			}
			execMounts := make([]bksolver.Result, len(mountData.Mounts))
			copy(execMounts, execInputs)
			for i, ref := range mountData.MountRefs {
				if ref == nil {
					continue
				}
				execMounts[i] = worker.NewWorkerRefResult(ref.Clone(), opWorker)
			}
			for _, active := range p.Actives {
				if active.NoCommit {
					active.Ref.Release(context.WithoutCancel(ctx))
				} else {
					ref, cerr := active.Ref.Commit(ctx)
					if cerr != nil {
						rerr = errors.Join(rerr, fmt.Errorf("error committing %s: %w: %w", active.Ref.ID(), cerr, err))
						continue
					}
					if active.MountIndex < 0 || active.MountIndex >= len(execMounts) {
						rerr = errors.Join(rerr, fmt.Errorf("active mount index %d outside exec mounts (%d)", active.MountIndex, len(execMounts)))
						continue
					}
					execMounts[active.MountIndex] = worker.NewWorkerRefResult(ref, opWorker)
				}
			}

			rerr = errdefs.WithExecError(rerr, execInputs, execMounts)
			rerr = buildkit.RichError{
				ExecError: rerr.(*errdefs.ExecError),
				Origin:    causeCtx,
				Mounts:    mountData.Mounts,
				ExecMD:    execMD,
				Meta:      metaSpec,
				Terminal: func(ctx context.Context, richErr *buildkit.RichError) error {
					return container.TerminalError(ctx, richErr.ExecMD.CallID, richErr)
				},
			}
		} else {
			// Only release actives if err is nil.
			for i := len(p.Actives) - 1; i >= 0; i-- { // call in LIFO order
				p.Actives[i].Ref.Release(context.WithoutCancel(ctx))
			}
		}
		for _, o := range p.OutputRefs {
			if o.Ref != nil {
				o.Ref.Release(context.WithoutCancel(ctx))
			}
		}
	}()

	// NOTE: seems to be a longstanding bug in buildkit that selector on root mount doesn't work, fix here
	for _, mnt := range mountData.Mounts {
		if mnt.Dest != "/" {
			continue
		}
		p.Root.Selector = mnt.Selector
		break
	}

	emu, err := getEmulator(ctx, specs.Platform(container.Platform))
	if err != nil {
		return nil, err
	}
	if emu != nil {
		metaSpec.Args = append([]string{buildkit.BuildkitQemuEmulatorMountPoint}, metaSpec.Args...)
		p.Mounts = append(p.Mounts, executor.Mount{
			Readonly: true,
			Src:      emu,
			Dest:     buildkit.BuildkitQemuEmulatorMountPoint,
		})
	}

	meta := *metaSpec
	meta.Env = slices.Clone(meta.Env)
	secretEnv, err := loadSecretEnv(ctx, bkSessionGroup, session, secretEnvs)
	if err != nil {
		return nil, err
	}
	meta.Env = append(meta.Env, secretEnv...)

	svcs, err := query.Services(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get services: %w", err)
	}
	detach, _, err := svcs.StartBindings(ctx, container.Services)
	if err != nil {
		return nil, err
	}
	defer detach()

	execWorker := opWorker.ExecWorker(causeCtx, *execMD)
	exec := execWorker.Executor()
	procInfo := executor.ProcessInfo{Meta: meta}
	if opts.Stdin != "" {
		// Stdin/Stdout/Stderr can be setup in Worker.setupStdio
		procInfo.Stdin = io.NopCloser(strings.NewReader(opts.Stdin))
	}
	_, execErr := exec.Run(ctx, "", p.Root, p.Mounts, procInfo, nil)

	for i, ref := range p.OutputRefs {
		// commit all refs
		var err error
		var iref bkcache.ImmutableRef
		if mutable, ok := ref.Ref.(bkcache.MutableRef); ok {
			iref, err = mutable.Commit(ctx)
			if err != nil {
				return nil, fmt.Errorf("error committing %s: %w", mutable.ID(), err)
			}
		} else {
			iref = ref.Ref.(bkcache.ImmutableRef)
		}

		// put the ref to the right mount point
		switch ref.MountIndex {
		case 0:
			rootfsDir := &Directory{
				Snapshot: iref,
			}
			if container.FS != nil {
				rootfsDir.Dir = container.FS.Self().Dir
				rootfsDir.Platform = container.FS.Self().Platform
				rootfsDir.Services = container.FS.Self().Services
			} else {
				rootfsDir.Dir = "/"
			}
			updatedRootFS, err := UpdatedRootFS(ctx, container, rootfsDir)
			if err != nil {
				return nil, fmt.Errorf("failed to update rootfs: %w", err)
			}
			container.FS = updatedRootFS

		case 1:
			container.Meta = &Directory{
				Snapshot: iref,
			}

		default:
			mountIdx := ref.MountIndex - 2
			if mountIdx < 0 || mountIdx >= len(container.Mounts) {
				return nil, errors.Join(rerr, fmt.Errorf("output mount index %d maps outside container mounts (%d)", ref.MountIndex, len(container.Mounts)))
			}
			ctrMnt := container.Mounts[mountIdx]

			err = handleMountValue(ctrMnt,
				func(dirMnt *dagql.ObjectResult[*Directory]) error {
					dir := &Directory{
						Snapshot: iref,
						Dir:      dirMnt.Self().Dir,
						Platform: dirMnt.Self().Platform,
						Services: dirMnt.Self().Services,
					}
					ctrMnt.DirectorySource, err = updatedDirMount(ctx, container, dir, ctrMnt.Target)
					if err != nil {
						return fmt.Errorf("failed to update directory mount: %w", err)
					}
					container.Mounts[mountIdx] = ctrMnt
					return nil
				},
				func(fileMnt *dagql.ObjectResult[*File]) error {
					file := &File{
						Snapshot: iref,
						File:     fileMnt.Self().File,
						Platform: fileMnt.Self().Platform,
						Services: fileMnt.Self().Services,
					}
					ctrMnt.FileSource, err = updatedFileMount(ctx, container, file, ctrMnt.Target)
					if err != nil {
						return fmt.Errorf("failed to update file mount: %w", err)
					}
					container.Mounts[mountIdx] = ctrMnt
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
				return nil, err
			}
		}

		// prevent the result from being released by the defer
		p.OutputRefs[i].Ref = nil
	}

	if execErr != nil {
		slog.Warn("process did not complete successfully",
			"process", strings.Join(metaSpec.Args, " "),
			"error", execErr)
		return nil, execErr
	}

	return container, nil
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

func (container *Container) usedClientID(ctx context.Context) (string, error) {
	return container.metaFileContents(ctx, buildkit.MetaMountClientIDPath)
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
