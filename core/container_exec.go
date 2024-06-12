package core

import (
	"context"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/identity"
	"github.com/pkg/errors"
)

type ContainerExecOpts struct {
	// Command to run instead of the container's default command
	Args []string

	// If the container has an entrypoint, ignore it for this exec rather than
	// calling it with args.
	SkipEntrypoint bool `default:"false"`

	// Content to write to the command's standard input before closing
	Stdin string `default:""`

	// Redirect the command's standard output to a file in the container
	RedirectStdout string `default:""`

	// Redirect the command's standard error to a file in the container
	RedirectStderr string `default:""`

	// Provide the executed command access back to the Dagger API
	ExperimentalPrivilegedNesting bool `default:"false"`

	// Grant the process all root capabilities
	InsecureRootCapabilities bool `default:"false"`

	// (Internal-only) If this is a nested exec, exec metadata to use for it
	NestedExecMetadata *buildkit.ExecutionMetadata `name:"-"`
}

func (container *Container) WithExec(ctx context.Context, opts ContainerExecOpts) (*Container, error) { //nolint:gocyclo
	container = container.Clone()

	cfg := container.Config
	mounts := container.Mounts
	platform := container.Platform
	if platform.OS == "" {
		platform = container.Query.Platform
	}

	args, err := container.command(opts)
	if err != nil {
		return nil, err
	}

	spanName := fmt.Sprintf("exec %s", strings.Join(args, " "))

	runOpts := []llb.RunOption{
		llb.Args(args),
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	execMD := buildkit.ExecutionMetadata{}
	if opts.NestedExecMetadata != nil {
		execMD = *opts.NestedExecMetadata
	}
	execMD.SessionID = clientMetadata.SessionID
	if execMD.HostAliases == nil {
		execMD.HostAliases = make(map[string][]string)
	}
	execMD.RedirectStdoutPath = opts.RedirectStdout
	execMD.RedirectStderrPath = opts.RedirectStderr
	execMD.SystemEnvNames = container.SystemEnvNames
	execMD.EnabledGPUs = container.EnabledGPUs

	// if GPU parameters are set for this container pass them over:
	if len(execMD.EnabledGPUs) > 0 {
		if gpuSupportEnabled := os.Getenv("_EXPERIMENTAL_DAGGER_GPU_SUPPORT"); gpuSupportEnabled == "" {
			return nil, fmt.Errorf("GPU support is not enabled, set _EXPERIMENTAL_DAGGER_GPU_SUPPORT")
		}
	}

	// this allows executed containers to communicate back to this API
	if opts.ExperimentalPrivilegedNesting {
		// directing telemetry to another span (i.e. a function call).
		if execMD.SpanContext != nil {
			// hide the exec span
			spanName = buildkit.InternalPrefix + spanName
		}

		execMD.ClientID = identity.NewID()

		// include the engine version so that these execs get invalidated if the engine/API change
		runOpts = append(runOpts, llb.AddEnv(buildkit.DaggerEngineVersionEnv, engine.Version))

		// include a digest of the current call so that we scope of the cache of the ExecOp to this call
		runOpts = append(runOpts, llb.AddEnv(buildkit.DaggerCallDigestEnv, string(dagql.CurrentID(ctx).Digest())))

		if execMD.CachePerSession {
			// include the SessionID here so that we bust cache once-per-session
			clientMetadata, err := engine.ClientMetadataFromContext(ctx)
			if err != nil {
				return nil, err
			}
			runOpts = append(runOpts, llb.AddEnv(buildkit.DaggerSessionIDEnv, clientMetadata.SessionID))
		}
	}

	runOpts = append(runOpts, llb.WithCustomName(spanName))

	metaSt, metaSourcePath := metaMount(opts.Stdin)

	// create mount point for the executor to write stdout/stderr/exitcode to
	runOpts = append(runOpts,
		llb.AddMount(buildkit.MetaMountDestPath, metaSt, llb.SourcePath(metaSourcePath)))

	if opts.RedirectStdout != "" {
		// ensure this path is in the cache key
		runOpts = append(runOpts, llb.AddEnv(buildkit.DaggerRedirectStdoutEnv, opts.RedirectStdout))
	}

	if opts.RedirectStderr != "" {
		// ensure this path is in the cache key
		runOpts = append(runOpts, llb.AddEnv(buildkit.DaggerRedirectStderrEnv, opts.RedirectStderr))
	}

	var aliasStrs []string
	for _, bnd := range container.Services {
		for _, alias := range bnd.Aliases {
			execMD.HostAliases[bnd.Hostname] = append(execMD.HostAliases[bnd.Hostname], alias)
			aliasStrs = append(aliasStrs, bnd.Hostname+"="+alias)
		}
	}
	if len(aliasStrs) > 0 {
		// ensure these are in the cache key, sort them for stability
		slices.Sort(aliasStrs)
		runOpts = append(runOpts,
			llb.AddEnv(buildkit.DaggerHostnameAliasesEnv, strings.Join(aliasStrs, ",")))
	}

	if cfg.User != "" {
		runOpts = append(runOpts, llb.User(cfg.User))
	}

	if cfg.WorkingDir != "" {
		runOpts = append(runOpts, llb.Dir(cfg.WorkingDir))
	}

	for _, env := range cfg.Env {
		name, val, ok := strings.Cut(env, "=")
		if !ok {
			// it's OK to not be OK
			// we'll just set an empty env
			_ = ok
		}

		runOpts = append(runOpts, llb.AddEnv(name, val))
	}

	for i, secret := range container.Secrets {
		secretOpts := []llb.SecretOption{llb.SecretID(secret.Secret.Accessor)}

		var secretDest string
		switch {
		case secret.EnvName != "":
			secretDest = secret.EnvName
			secretOpts = append(secretOpts, llb.SecretAsEnv(true))
			execMD.SecretEnvNames = append(execMD.SecretEnvNames, secret.EnvName)
		case secret.MountPath != "":
			secretDest = secret.MountPath
			execMD.SecretFilePaths = append(execMD.SecretFilePaths, secret.MountPath)
			if secret.Owner != nil {
				secretOpts = append(secretOpts, llb.SecretFileOpt(
					secret.Owner.UID,
					secret.Owner.GID,
					int(secret.Mode),
				))
			}
		default:
			return nil, fmt.Errorf("malformed secret config at index %d", i)
		}

		runOpts = append(runOpts, llb.AddSecret(secretDest, secretOpts...))
	}

	for _, ctrSocket := range container.Sockets {
		if ctrSocket.ContainerPath == "" {
			return nil, fmt.Errorf("unsupported socket: only unix paths are implemented")
		}

		socketOpts := []llb.SSHOption{
			llb.SSHID(ctrSocket.Source.SSHID()),
			llb.SSHSocketTarget(ctrSocket.ContainerPath),
		}

		if ctrSocket.Owner != nil {
			socketOpts = append(socketOpts,
				llb.SSHSocketOpt(
					ctrSocket.ContainerPath,
					ctrSocket.Owner.UID,
					ctrSocket.Owner.GID,
					0o600, // preserve default
				))
		}

		runOpts = append(runOpts, llb.AddSSHSocket(socketOpts...))
	}

	for _, mnt := range mounts {
		srcSt, err := mnt.SourceState()
		if err != nil {
			return nil, fmt.Errorf("mount %s: %w", mnt.Target, err)
		}

		mountOpts := []llb.MountOption{}

		if mnt.SourcePath != "" {
			mountOpts = append(mountOpts, llb.SourcePath(mnt.SourcePath))
		}

		if mnt.CacheVolumeID != "" {
			var sharingMode llb.CacheMountSharingMode
			switch mnt.CacheSharingMode {
			case CacheSharingModeShared:
				sharingMode = llb.CacheMountShared
			case CacheSharingModePrivate:
				sharingMode = llb.CacheMountPrivate
			case CacheSharingModeLocked:
				sharingMode = llb.CacheMountLocked
			default:
				return nil, errors.Errorf("invalid cache mount sharing mode %q", mnt.CacheSharingMode)
			}

			mountOpts = append(mountOpts, llb.AsPersistentCacheDir(mnt.CacheVolumeID, sharingMode))
		}

		if mnt.Tmpfs {
			mountOpts = append(mountOpts, llb.Tmpfs())
		}

		if mnt.Readonly {
			mountOpts = append(mountOpts, llb.Readonly)
		}

		runOpts = append(runOpts, llb.AddMount(mnt.Target, srcSt, mountOpts...))
	}

	if opts.InsecureRootCapabilities {
		runOpts = append(runOpts, llb.Security(llb.SecurityModeInsecure))
	}

	fsSt, err := container.FSState()
	if err != nil {
		return nil, fmt.Errorf("fs state: %w", err)
	}

	execMDOpt, err := execMD.AsConstraintsOpt()
	if err != nil {
		return nil, fmt.Errorf("execution metadata: %w", err)
	}
	runOpts = append(runOpts, execMDOpt)
	execSt := fsSt.Run(runOpts...)

	marshalOpts := []llb.ConstraintsOpt{
		llb.Platform(platform.Spec()),
		execMDOpt,
	}
	execDef, err := execSt.Root().Marshal(ctx, marshalOpts...)
	if err != nil {
		return nil, fmt.Errorf("marshal root: %w", err)
	}

	container.FS = execDef.ToPB()

	metaDef, err := execSt.GetMount(buildkit.MetaMountDestPath).Marshal(ctx, marshalOpts...)
	if err != nil {
		return nil, fmt.Errorf("get meta mount: %w", err)
	}

	container.Meta = metaDef.ToPB()

	for i, mnt := range mounts {
		if mnt.Tmpfs || mnt.CacheVolumeID != "" {
			continue
		}

		mountSt := execSt.GetMount(mnt.Target)

		// propagate any changes to regular mounts to subsequent containers
		execMountDef, err := mountSt.Marshal(ctx, marshalOpts...)
		if err != nil {
			return nil, fmt.Errorf("propagate %s: %w", mnt.Target, err)
		}

		mounts[i].Source = execMountDef.ToPB()
	}

	container.Mounts = mounts

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) MetaFileContents(ctx context.Context, filePath string) (string, error) {
	if container.Meta == nil {
		ctr, err := container.WithExec(ctx, ContainerExecOpts{})
		if err != nil {
			return "", err
		}
		return ctr.MetaFileContents(ctx, filePath)
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

func metaMount(stdin string) (llb.State, string) {
	meta := llb.Mkdir(buildkit.MetaMountDestPath, 0o777)
	if stdin != "" {
		meta = meta.Mkfile(path.Join(buildkit.MetaMountDestPath, buildkit.MetaMountStdinPath), 0o666, []byte(stdin))
	}

	return llb.Scratch().File(
			meta,
			llb.WithCustomName(buildkit.InternalPrefix+"creating dagger metadata"),
		),
		buildkit.MetaMountDestPath
}
