package schema

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core/filesystem"
	"github.com/dagger/dagger/core/shim"
	"github.com/dagger/dagger/router"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

type Exec struct {
	FS       *filesystem.Filesystem
	Metadata *filesystem.Filesystem
	Mounts   map[string]*filesystem.Filesystem
}

type MountInput struct {
	Path string
	FS   filesystem.FSID
}

type CacheMountInput struct {
	Name        string
	SharingMode string // TODO:(sipsma) switch to enum
	Path        string
}

type ExecInput struct {
	Args        []string
	Mounts      []MountInput
	CacheMounts []CacheMountInput
	Workdir     string
	Env         []ExecEnvInput
	SecretEnv   []ExecSecretEnvInput
	SSHAuthSock string `json:"sshAuthSock,omitempty"`
}

type ExecEnvInput struct {
	Name  string
	Value string
}

type ExecSecretEnvInput struct {
	Name string
	ID   string `json:"id"` // TODO:(sipsma) stronger type
}

var _ router.ExecutableSchema = &filesystemSchema{}

type execSchema struct {
	*baseSchema
}

func (s *execSchema) Name() string {
	return "core"
}

func (s *execSchema) Schema() string {
	return `
"Command execution"
type Exec {
	"Modified filesystem"
	fs: Filesystem!

	"stdout of the command"
	stdout(lines: Int): String

	"stderr of the command"
	stderr(lines: Int): String

	"Exit code of the command"
	exitCode: Int

	"Modified mounted filesystem"
	mount(path: String!): Filesystem!
}

input MountInput {
	"filesystem to mount"
	fs: FSID!

	"path at which the filesystem will be mounted"
	path: String!
}

input CacheMountInput {
	"Cache mount name"
	name: String!

	"Cache mount sharing mode (TODO: switch to enum)"
	sharingMode: String!

	"path at which the cache will be mounted"
	path: String!
}

input ExecInput {
	"""
	Command to execute
	Example: ["echo", "hello, world!"]
	"""
	args: [String!]!

	"Filesystem mounts"
	mounts: [MountInput!]

	"Cached mounts"
	cacheMounts: [CacheMountInput!]

	"Working directory"
	workdir: String

	"Env vars"
	env: [ExecEnvInput!]

	"Secret env vars"
	secretEnv: [ExecSecretEnvInput!]

	"Include the host's ssh agent socket in the exec at the provided path"
	sshAuthSock: String
}

input ExecEnvInput {
	"Env var name"
	name: String!
	"Env var value"
	value: String!
}

input ExecSecretEnvInput {
	"Env var name"
	name: String!
	"Secret env var value"
	id: SecretID!
}

extend type Filesystem {
	"execute a command inside this filesystem"
 	exec(input: ExecInput!): Exec!
}
	`
}

func (s *execSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Filesystem": router.ObjectResolver{
			"exec": router.ToResolver(s.exec),
		},
		"Exec": router.ObjectResolver{
			"stdout":   router.ToResolver(s.stdout),
			"stderr":   router.ToResolver(s.stderr),
			"exitCode": router.ToResolver(s.exitCode),
			"mount":    router.ToResolver(s.mount),
		},
	}
}

func (s *execSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type execArgs struct {
	Input ExecInput
}

func (s *execSchema) exec(ctx *router.Context, parent *filesystem.Filesystem, args execArgs) (*Exec, error) {
	// cleanup mount paths for consistency
	for i := range args.Input.Mounts {
		args.Input.Mounts[i].Path = filepath.Clean(args.Input.Mounts[i].Path)
	}

	shimSt, err := shim.Build(ctx, s.gw, s.platform)
	if err != nil {
		return nil, err
	}

	runOpt := []llb.RunOption{
		llb.Args(append([]string{shim.Path}, args.Input.Args...)),
		llb.AddMount(shim.Path, shimSt, llb.SourcePath(shim.Path)),
		llb.Dir(args.Input.Workdir),
		llb.WithCustomName(strings.Join(args.Input.Args, " ")),
	}
	for _, cacheMount := range args.Input.CacheMounts {
		var sharingMode llb.CacheMountSharingMode
		switch cacheMount.SharingMode {
		case "shared":
			sharingMode = llb.CacheMountShared
		case "private":
			sharingMode = llb.CacheMountPrivate
		case "locked":
			sharingMode = llb.CacheMountLocked
		default:
			return nil, errors.Errorf("invalid cache mount sharing mode %q", cacheMount.SharingMode)
		}
		runOpt = append(runOpt, llb.AddMount(
			cacheMount.Path,
			llb.Scratch(),
			llb.AsPersistentCacheDir(cacheMount.Name, sharingMode),
		))
	}

	for _, env := range args.Input.Env {
		runOpt = append(runOpt, llb.AddEnv(env.Name, env.Value))
	}
	for _, secretEnv := range args.Input.SecretEnv {
		runOpt = append(runOpt, llb.AddSecret(secretEnv.Name, llb.SecretID(secretEnv.ID), llb.SecretAsEnv(true)))
	}

	// FIXME:(sipsma) this should be generalized when support for service sockets are added, not hardcoded into the schema
	if args.Input.SSHAuthSock != "" {
		runOpt = append(runOpt,
			llb.AddSSHSocket(
				llb.SSHID(s.sshAuthSockID),
				llb.SSHSocketTarget(args.Input.SSHAuthSock),
			),
			llb.AddEnv("SSH_AUTH_SOCK", args.Input.SSHAuthSock),
		)
	}

	st, err := parent.ToState()
	if err != nil {
		return nil, err
	}
	execState := st.Run(runOpt...)

	// Metadata mount (used by the shim)
	_ = execState.AddMount("/dagger", llb.Scratch())

	for _, mount := range args.Input.Mounts {
		mountFS := &filesystem.Filesystem{
			ID: mount.FS,
		}
		state, err := mountFS.ToState()
		if err != nil {
			return nil, err
		}
		_ = execState.AddMount(mount.Path, state)
	}

	fs, err := s.Solve(ctx, execState.Root())
	if err != nil {
		// clean up shim from error messages
		cleanErr := strings.ReplaceAll(err.Error(), shim.Path+" ", "")
		return nil, errors.New(cleanErr)
	}

	metadataFS, err := filesystem.FromState(ctx, execState.GetMount("/dagger"), s.platform)
	if err != nil {
		return nil, err
	}

	mounts := map[string]*filesystem.Filesystem{}
	for _, mount := range args.Input.Mounts {
		mountFS, err := filesystem.FromState(ctx, execState.GetMount(mount.Path), s.platform)
		if err != nil {
			return nil, err
		}
		mounts[mount.Path] = mountFS
	}

	return &Exec{
		FS:       fs,
		Metadata: metadataFS,
		Mounts:   mounts,
	}, nil
}

type stdouterrArgs struct {
	Lines *int
}

func (s *execSchema) stdout(ctx *router.Context, parent *Exec, args stdouterrArgs) (string, error) {
	output, err := parent.Metadata.ReadFile(ctx, s.gw, "/stdout")
	if err != nil {
		return "", err
	}

	return truncate(string(output), args.Lines), nil
}

func (s *execSchema) stderr(ctx *router.Context, parent *Exec, args stdouterrArgs) (string, error) {
	output, err := parent.Metadata.ReadFile(ctx, s.gw, "/stderr")
	if err != nil {
		return "", err
	}

	return truncate(string(output), args.Lines), nil
}

func (s *execSchema) exitCode(ctx *router.Context, parent *Exec, args any) (int, error) {
	output, err := parent.Metadata.ReadFile(ctx, s.gw, "/exitCode")
	if err != nil {
		return 0, err
	}

	return strconv.Atoi(string(output))
}

type mountArgs struct {
	Path string
}

func (s *execSchema) mount(ctx *router.Context, parent *Exec, args mountArgs) (*filesystem.Filesystem, error) {
	path := filepath.Clean(args.Path)
	mnt, ok := parent.Mounts[path]
	if !ok {
		return nil, fmt.Errorf("missing mount path")
	}
	return mnt, nil
}

func truncate(s string, lines *int) string {
	if lines == nil {
		return s
	}
	l := strings.SplitN(s, "\n", *lines+1)
	if *lines > len(l) {
		*lines = len(l)
	}
	return strings.Join(l[0:*lines], "\n")
}
