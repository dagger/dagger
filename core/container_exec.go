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

	bkcache "github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/executor"
	bkcontainer "github.com/moby/buildkit/frontend/gateway/container"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets"
	bksolver "github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/llbsolver/errdefs"
	bkmounts "github.com/moby/buildkit/solver/llbsolver/mounts"
	"github.com/moby/buildkit/solver/pb"
	utilsystem "github.com/moby/buildkit/util/system"
	"github.com/moby/buildkit/worker"
	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/network"
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
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current query: %w", err)
	}

	execMD := buildkit.ExecutionMetadata{}
	if parent != nil {
		execMD = *parent
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
	if execMD.EncodedModuleID == "" {
		mod, err := query.CurrentModule(ctx)
		if err != nil {
			if !errors.Is(err, ErrNoCurrentModule) {
				return nil, err
			}
		} else {
			if mod.ResultID == nil {
				return nil, fmt.Errorf("current module has no instance ID")
			}
			execMD.EncodedModuleID, err = mod.ResultID.Encode()
			if err != nil {
				return nil, err
			}
		}
	}

	if execMD.HostAliases == nil {
		execMD.HostAliases = make(map[string][]string)
	}
	execMD.RedirectStdoutPath = opts.RedirectStdout
	execMD.RedirectStderrPath = opts.RedirectStderr
	execMD.SystemEnvNames = container.SystemEnvNames
	execMD.EnabledGPUs = container.EnabledGPUs
	if opts.NoInit {
		execMD.NoInit = true
	}

	if execMD.EncodedModuleID != "" {
		modID := new(call.ID)
		if err := modID.Decode(execMD.EncodedModuleID); err != nil {
			return nil, fmt.Errorf("failed to decode module ID: %w", err)
		}

		// allow the exec to reach services scoped to the module that installed it
		execMD.ExtraSearchDomains = append(execMD.ExtraSearchDomains, network.ModuleDomain(modID, clientMetadata.SessionID))
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
				ID:   secret.Secret.ID().Digest().String(),
				Name: secret.EnvName,
			})
		}
	}
	return secretEnvs
}

func (container *Container) WithExec(ctx context.Context, opts ContainerExecOpts, parent *buildkit.ExecutionMetadata) (_ *Container, rerr error) { //nolint:gocyclo
	container = container.Clone()

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current query: %w", err)
	}

	platform := container.Platform
	if platform.OS == "" {
		platform = query.Platform()
	}

	secretEnvs := container.secretEnvs()

	execMD, err := container.execMeta(ctx, opts, parent)
	if err != nil {
		return nil, err
	}

	mounts, ok := CurrentMountData(ctx)
	if !ok {
		return nil, fmt.Errorf("no dagop here")
	}

	workerRefs := make([]*worker.WorkerRef, 0, len(mounts.Inputs))
	for _, ref := range mounts.InputRefs() {
		workerRefs = append(workerRefs, &worker.WorkerRef{ImmutableRef: ref})
	}

	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}
	cache := query.BuildkitCache()
	session := query.BuildkitSession()

	metaSpec, err := container.metaSpec(ctx, opts)
	if err != nil {
		return nil, err
	}

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit session group in context")
	}

	opt, ok := buildkit.CurrentOpOpts(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit opts in context")
	}

	mm := bkmounts.NewMountManager(fmt.Sprintf("exec %s", strings.Join(metaSpec.Args, " ")), cache, session)
	p, err := bkcontainer.PrepareMounts(ctx, mm, cache, bkSessionGroup, container.Config.WorkingDir, mounts.Mounts, workerRefs, func(m *pb.Mount, ref bkcache.ImmutableRef) (bkcache.MutableRef, error) {
		desc := fmt.Sprintf("mount %s from exec %s", m.Dest, strings.Join(metaSpec.Args, " "))
		return cache.New(ctx, ref, bkSessionGroup, bkcache.WithDescription(desc))
	}, runtime.GOOS)
	defer func() {
		if rerr != nil {
			execInputs := make([]bksolver.Result, len(mounts.Mounts))
			for i, m := range mounts.Mounts {
				if m.Input == pb.Empty {
					continue
				}
				execInputs[i] = mounts.Inputs[m.Input].Clone()
			}
			execMounts := make([]bksolver.Result, len(mounts.Mounts))
			copy(execMounts, execInputs)
			results, err := extractContainerBkOutputs(ctx, container, bk, opt.Worker, mounts)
			if err != nil {
				return
			}
			for i, res := range results {
				execMounts[p.OutputRefs[i].MountIndex] = res
			}
			for _, active := range p.Actives {
				if active.NoCommit {
					active.Ref.Release(context.WithoutCancel(ctx))
				} else {
					ref, cerr := active.Ref.Commit(ctx)
					if cerr != nil {
						rerr = fmt.Errorf("error committing %s: %w: %w", active.Ref.ID(), cerr, err)
						continue
					}
					execMounts[active.MountIndex] = worker.NewWorkerRefResult(ref, opt.Worker)
				}
			}

			rerr = errdefs.WithExecError(rerr, execInputs, execMounts)
			rerr = buildkit.RichError{
				ExecError: rerr.(*errdefs.ExecError),
				Origin:    opt.CauseCtx,
				Mounts:    mounts.Mounts,
				ExecMD:    execMD,
				Meta:      metaSpec,
				Secretenv: secretEnvs,
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
	if err != nil {
		return nil, err
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

	worker := opt.Worker.(*buildkit.Worker)
	worker = worker.ExecWorker(opt.CauseCtx, *execMD)
	exec := worker.Executor()

	svcs, err := query.Services(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get services: %w", err)
	}
	detach, _, err := svcs.StartBindings(ctx, container.Services)
	if err != nil {
		return nil, err
	}
	defer detach()

	_, execErr := exec.Run(ctx, "", p.Root, p.Mounts, executor.ProcessInfo{
		Meta:  meta,
		Stdin: io.NopCloser(strings.NewReader(opts.Stdin)),
		// Stdout/Stderr are setup in Worker.setupStdio
	}, nil)

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
			container.FSResult = iref
		case 1:
			container.MetaResult = iref
		default:
			mountIdx := ref.MountIndex - 2
			if mountIdx >= len(container.Mounts) {
				// something is disastourously wrong, panic!
				panic(fmt.Sprintf("index %d escapes number of mounts %d", mountIdx, len(container.Mounts)))
			}
			container.Mounts[mountIdx].Result = iref
		}

		// prevent the result from being released by the defer
		p.OutputRefs[i].Ref = nil
	}

	if execErr != nil {
		return nil, fmt.Errorf("process %q did not complete successfully: %w", strings.Join(metaSpec.Args, " "), execErr)
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

func (container *Container) UsedClientID(ctx context.Context) (string, error) {
	return container.metaFileContents(ctx, buildkit.MetaMountClientIDPath)
}

func (container *Container) metaFileContents(ctx context.Context, filePath string) (string, error) {
	if container.Meta == nil {
		return "", ErrNoCommand
	}
	file := NewFile(
		container.Meta,
		filePath,
		container.Platform,
		container.Services,
	)
	content, err := file.ContentsDagOp(ctx)
	if err != nil {
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
