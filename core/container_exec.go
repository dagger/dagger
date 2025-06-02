package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	bkcache "github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/client/llb"
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

	// (Internal-only) If this is a nested exec, exec metadata to use for it
	// NestedExecMetadata        *buildkit.ExecutionMetadata `name:"-"`
	// NestedExecMetadata string `default:""`

	// Expand the environment variables in args
	Expand bool `default:"false"`

	// Skip the init process injected into containers by default so that the
	// user's process is PID 1
	NoInit bool `default:"false"`
}

func (container *Container) execMeta(ctx context.Context, opts ContainerExecOpts) (*buildkit.ExecutionMetadata, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	execMD := buildkit.ExecutionMetadata{}
	if md := buildkit.ExecutionMetadataFromContext(ctx); md != nil {
		execMD = *md
	}
	// if mdEncoded := opts.NestedExecMetadata; mdEncoded != "" {
	// 	err := json.Unmarshal([]byte(mdEncoded), &execMD)
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// }
	// if opts.NestedExecMetadata != nil {
	// 	execMD = *opts.NestedExecMetadata
	// }
	if execMD.CallID == nil {
		execMD.CallID = dagql.CurrentID(ctx) // XXX: this is actually wrong
	}
	execMD.CallerClientID = clientMetadata.ClientID
	execMD.ExecID = identity.NewID()
	execMD.SessionID = clientMetadata.SessionID
	execMD.AllowedLLMModules = clientMetadata.AllowedLLMModules
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

	mod, err := container.Query.CurrentModule(ctx)
	if err == nil {
		if mod.InstanceID == nil {
			return nil, fmt.Errorf("current module has no instance ID")
		}
		// allow the exec to reach services scoped to the module that
		// installed it
		execMD.ExtraSearchDomains = append(execMD.ExtraSearchDomains,
			network.ModuleDomain(mod.InstanceID, clientMetadata.SessionID))
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

	fmt.Println("client", execMD.ClientID, execMD.SecretToken)
	return &execMD, nil
}

func (container *Container) metaSpec(ctx context.Context, opts ContainerExecOpts) (*executor.Meta, error) {
	cfg := container.Config
	args, err := container.command(opts)
	if err != nil {
		return nil, err
	}
	platform := container.Platform
	if platform.OS == "" {
		platform = container.Query.Platform()
	}

	op, ok := DagOpFromContext[ContainerDagOp](ctx)
	if !ok {
		return nil, fmt.Errorf("no dagop")
	}

	metaSpec := executor.Meta{
		Args: args,
		Env:  slices.Clone(cfg.Env),
		Cwd:  cmp.Or(cfg.WorkingDir, "/"),
		User: cfg.User,
		// Hostname:       e.op.Meta.Hostname, // empty seems right?
		ReadonlyRootFS: op.Mounts[0].Readonly,
		// ExtraHosts:     extraHosts,
		// Ulimit:                    e.op.Meta.Ulimit,
		// CgroupParent:              e.op.Meta.CgroupParent,
		// NetMode:                   e.op.Network,
		RemoveMountStubsRecursive: true,
	}
	if opts.InsecureRootCapabilities {
		metaSpec.SecurityMode = pb.SecurityMode_INSECURE
	}

	// if e.op.Meta.ProxyEnv != nil {
	// 	meta.Env = append(meta.Env, proxyEnvList(e.op.Meta.ProxyEnv)...)
	// }
	metaSpec.Env = addDefaultEnvvar(metaSpec.Env, "PATH", utilsystem.DefaultPathEnv(platform.OS))

	if opts.Expect != ReturnSuccess {
		metaSpec.ValidExitCodes = opts.Expect.ReturnCodes()
	}

	return &metaSpec, nil
}

func (container *Container) WithExec(ctx context.Context, opts ContainerExecOpts) (_ *Container, rerr error) {
	container = container.Clone()

	platform := container.Platform
	if platform.OS == "" {
		platform = container.Query.Platform()
	}

	secretEnvs := []*pb.SecretEnv{}
	for _, secret := range container.Secrets {
		if secret.EnvName != "" {
			secretEnvs = append(secretEnvs, &pb.SecretEnv{
				ID:   secret.Secret.ID().Digest().String(),
				Name: secret.EnvName,
			})
		}
	}

	execMD, err := container.execMeta(ctx, opts)
	if err != nil {
		return nil, err
	}

	op, ok := DagOpFromContext[ContainerDagOp](ctx)
	if !ok {
		return nil, fmt.Errorf("no dagop")
	}

	refs := op.Inputs()
	workerRefs := make([]*worker.WorkerRef, 0, len(refs))
	for _, ref := range refs {
		// XXX: PrepareMounts needs this (but only uses ImmutableRef)
		workerRefs = append(workerRefs, &worker.WorkerRef{ImmutableRef: ref})
	}

	cache := container.Query.BuildkitCache()
	session := container.Query.BuildkitSession()

	metaSpec, err := container.metaSpec(ctx, opts)
	if err != nil {
		return nil, err
	}

	mm := bkmounts.NewMountManager(fmt.Sprintf("exec %s", strings.Join(metaSpec.Args, " ")), cache, session)
	p, err := bkcontainer.PrepareMounts(ctx, mm, cache, op.Group(), container.Config.WorkingDir, op.Mounts, workerRefs, func(m *pb.Mount, ref bkcache.ImmutableRef) (bkcache.MutableRef, error) {
		desc := fmt.Sprintf("mount %s from exec %s", m.Dest, strings.Join(metaSpec.Args, " "))
		return cache.New(ctx, ref, op.Group(), bkcache.WithDescription(desc))
	}, runtime.GOOS)
	defer func() {
		if rerr != nil {
			execInputs := make([]bksolver.Result, len(op.Mounts))
			for i, m := range op.Mounts {
				if m.Input == pb.Empty {
					continue
				}
				execInputs[i] = op.inputs[m.Input].Clone()
			}
			execMounts := make([]bksolver.Result, len(op.Mounts))
			copy(execMounts, execInputs)
			results, err := op.extractContainerBkOutputs(ctx, container, op.opt.Worker)
			if err != nil {
				return
			}
			for i, res := range results {
				execMounts[p.OutputRefs[i].MountIndex] = res
			}
			for _, active := range p.Actives {
				if active.NoCommit {
					active.Ref.Release(context.TODO())
				} else {
					ref, cerr := active.Ref.Commit(ctx)
					if cerr != nil {
						rerr = fmt.Errorf("error committing %s: %w: %w", active.Ref.ID(), cerr, err)
						continue
					}
					execMounts[active.MountIndex] = worker.NewWorkerRefResult(ref, op.opt.Worker)
				}
			}

			rerr = errdefs.WithExecError(rerr, execInputs, execMounts)
			rerr = buildkit.RichError{
				ExecError: rerr.(*errdefs.ExecError),
				Mounts:    op.Mounts,
				ExecMD:    execMD,
				Meta:      metaSpec,
				Secretenv: secretEnvs,
			}
		} else {
			// Only release actives if err is nil.
			for i := len(p.Actives) - 1; i >= 0; i-- { // call in LIFO order
				p.Actives[i].Ref.Release(context.TODO())
			}
		}
		for _, o := range p.OutputRefs {
			if o.Ref != nil {
				o.Ref.Release(context.TODO())
			}
		}
	}()
	if err != nil {
		return nil, err
	}

	meta := *metaSpec
	meta.Args = slices.Clone(meta.Args)
	meta.Env = slices.Clone(meta.Env)

	secretEnv, err := loadSecretEnv(ctx, op.Group(), session, secretEnvs)
	if err != nil {
		return nil, err
	}
	meta.Env = append(meta.Env, secretEnv...)

	emu, err := getEmulator(ctx, specs.Platform(container.Platform))
	if err != nil {
		return nil, err
	}
	if emu != nil {
		meta.Args = append([]string{qemuMountName}, meta.Args...)
		p.Mounts = append(p.Mounts, executor.Mount{
			Readonly: true,
			Src:      emu,
			Dest:     qemuMountName,
		})
	}

	// FIXME: this abstraction is now irrelevant - we don't need to do buildkit smuggling anymore
	worker := op.opt.Worker.(*buildkit.Worker)
	worker = worker.ExecWorker(op.opt.CauseCtx, *execMD)
	exec := worker.Executor()
	_, execErr := exec.Run(ctx, "", p.Root, p.Mounts, executor.ProcessInfo{
		Meta: meta,
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
			// XXX: this panics?
			// what is op.Mounts at *this point*?
			if ref.MountIndex-2 >= len(container.Mounts) {
				mnts, _ := json.Marshal(op.Mounts)
				out := strings.Join(meta.Args, " ") + "\n"
				out += fmt.Sprintf("out of range %d/%d\n%s\n", ref.MountIndex-2, len(container.Mounts), string(mnts))
				for i, mount := range container.Mounts {
					out += fmt.Sprintf("ctr mount %d %s", i, mount.Target)
					out += "\n"
				}
				for i, ref := range p.OutputRefs {
					out += fmt.Sprintf("output %d %d", i, ref.MountIndex)
					out += "\n"
				}
				panic(out)
			}
			// panic: runtime error: index out of range [2] with length 2
			// goroutine 19423727 [running]:
			// github.com/dagger/dagger/core.(*Container).WithExec(0x1?, {0x3980f50, 0xc1481f4b90}, {{0xc148589d60, 0xa, 0xa}, 0x0, {0x0, 0x0}, {0x0, ...}, ...})
			// 	/app/core/container_exec.go:362 +0x18ad
			// github.com/dagger/dagger/core/schema.(*containerSchema).withExec(0xc1170629d8, {0x3980f50, 0xc1481f4b90}, {0xc148680e40, 0xc08f1f58c8, {0x0, 0x1, 0xc1176631d0, 0xc11763c650}, 0x0, ...}, ...)
			// 	/app/core/schema/container.go:926 +0x9ee
			// github.com/dagger/dagger/dagql.NodeFuncWithCacheKey[...].func1({0xc148680e40, 0xc08f1f58c8, {0x0, 0x1, 0xc1176631d0, 0xc11763c650}, 0x0, 0x0}, 0xc144723500, {0xc12a0bd530, ...})
			// 	/app/dagql/objects.go:809 +0x165
			// github.com/dagger/dagger/dagql.Class[...].Call(0x39d3e00?, {0x3980f50?, 0xc1481f4b90?}, {0xc148680e40, 0xc08f1f58c8, {0x0, 0x1, 0xc1176631d0, 0xc11763c650}, 0x0, ...}, ...)
			// 	/app/dagql/objects.go:269 +0x152
			// github.com/dagger/dagger/dagql.Instance[...].call.func2()
			// 	/app/dagql/objects.go:568 +0xbf
			// github.com/dagger/dagger/engine/cache.(*cache[...]).GetOrInitializeWithCallbacks.func1()
			// 	/app/engine/cache/cache.go:210 +0x5f
			// created by github.com/dagger/dagger/engine/cache.(*cache[...]).GetOrInitializeWithCallbacks in goroutine 19423724
			// 	/app/engine/cache/cache.go:208 +0x68c
			container.Mounts[ref.MountIndex-2].Result = iref
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

// XXX:
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

func (container *Container) usedClientID(ctx context.Context) (string, error) {
	return container.metaFileContents(ctx, buildkit.MetaMountClientIDPath)
}

func (container *Container) metaFileContents(ctx context.Context, filePath string) (string, error) {
	if container.Meta == nil {
		return "", fmt.Errorf("%w: %s requires an exec", ErrNoCommand, filePath)
	}

	file := NewFile(
		container.Query,
		container.Meta,
		path.Join(buildkit.MetaMountDestPath, filePath),
		container.Platform,
		container.Services,
	)

	content, err := file.Contents(ctx)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

// XXX: clean this up! meta mount shouldn't be everywhere like this
func metaMount(ctx context.Context, stdin string) (llb.State, string) {
	meta := llb.Mkdir(buildkit.MetaMountDestPath, 0o777)
	if stdin != "" {
		meta = meta.Mkfile(path.Join(buildkit.MetaMountDestPath, buildkit.MetaMountStdinPath), 0o666, []byte(stdin))
	}

	return llb.Scratch().File(
			meta,
			buildkit.WithTracePropagation(ctx),
			buildkit.WithPassthrough(),
		),
		buildkit.MetaMountDestPath
}
