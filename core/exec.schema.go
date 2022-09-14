package core

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/core/shim"
	"github.com/dagger/cloak/router"
	"github.com/graphql-go/graphql"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

type Exec struct {
	FS       *filesystem.Filesystem            `json:"fs"`
	Metadata *filesystem.Filesystem            `json:"metadata"`
	Mounts   map[string]*filesystem.Filesystem `json:"mounts"`
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
	sshAuthSockID string
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
			"exec": s.exec,
		},
		"Exec": router.ObjectResolver{
			"stdout":   s.stdout,
			"stderr":   s.stderr,
			"exitCode": s.exitCode,
			"mount":    s.mount,
		},
	}
}

func (s *execSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

func (s *execSchema) exec(p graphql.ResolveParams) (any, error) {
	// TODO:
	parent := router.Parent[*filesystem.Filesystem](p.Source)
	obj, err := filesystem.FromSource(parent.Val)
	if err != nil {
		return nil, err
	}

	var input ExecInput
	if err := convertArg(p.Args["input"], &input); err != nil {
		return nil, err
	}

	// cleanup mount paths for consistency
	for i := range input.Mounts {
		input.Mounts[i].Path = filepath.Clean(input.Mounts[i].Path)
	}

	shimSt, err := shim.Build(p.Context, s.gw, parent.Platform)
	if err != nil {
		return nil, err
	}

	runOpt := []llb.RunOption{
		llb.Args(append([]string{shim.Path}, input.Args...)),
		llb.AddMount(shim.Path, shimSt, llb.SourcePath(shim.Path)),
		llb.Dir(input.Workdir),
		llb.WithCustomName(strings.Join(input.Args, " ")),
	}
	for _, cacheMount := range input.CacheMounts {
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

	for _, env := range input.Env {
		runOpt = append(runOpt, llb.AddEnv(env.Name, env.Value))
	}
	for _, secretEnv := range input.SecretEnv {
		runOpt = append(runOpt, llb.AddSecret(secretEnv.Name, llb.SecretID(secretEnv.ID), llb.SecretAsEnv(true)))
	}

	// FIXME:(sipsma) this should be generalized when support for service sockets are added, not hardcoded into the schema
	if input.SSHAuthSock != "" {
		runOpt = append(runOpt,
			llb.AddSSHSocket(
				llb.SSHID(s.sshAuthSockID),
				llb.SSHSocketTarget(input.SSHAuthSock),
			),
			llb.AddEnv("SSH_AUTH_SOCK", input.SSHAuthSock),
		)
	}

	st, err := obj.ToState()
	if err != nil {
		return nil, err
	}
	execState := st.Run(runOpt...)

	// Metadata mount (used by the shim)
	_ = execState.AddMount("/dagger", llb.Scratch())

	for _, mount := range input.Mounts {
		mountFS := &filesystem.Filesystem{
			ID: mount.FS,
		}
		state, err := mountFS.ToState()
		if err != nil {
			return nil, err
		}
		_ = execState.AddMount(mount.Path, state)
	}

	fs, err := s.Solve(p.Context, execState.Root(), parent.Platform)
	if err != nil {
		// clean up shim from error messages
		cleanErr := strings.ReplaceAll(err.Error(), shim.Path+" ", "")
		return nil, errors.New(cleanErr)
	}

	metadataFS, err := filesystem.FromState(p.Context, execState.GetMount("/dagger"), parent.Platform)
	if err != nil {
		return nil, err
	}

	mounts := map[string]*filesystem.Filesystem{}
	for _, mount := range input.Mounts {
		mountFS, err := filesystem.FromState(p.Context, execState.GetMount(mount.Path), parent.Platform)
		if err != nil {
			return nil, err
		}
		mounts[mount.Path] = mountFS
	}

	return router.WithVal(parent, &Exec{
		FS:       fs,
		Metadata: metadataFS,
		Mounts:   mounts,
	}), nil
}

func (s *execSchema) stdout(p graphql.ResolveParams) (any, error) {
	parent := router.Parent[*Exec](p.Source)
	output, err := parent.Val.Metadata.ReadFile(p.Context, s.gw, "/stdout")
	if err != nil {
		return nil, err
	}

	return truncate(string(output), p.Args), nil
}

func (s *execSchema) stderr(p graphql.ResolveParams) (any, error) {
	parent := router.Parent[*Exec](p.Source)
	output, err := parent.Val.Metadata.ReadFile(p.Context, s.gw, "/stderr")
	if err != nil {
		return nil, err
	}

	return truncate(string(output), p.Args), nil
}

func (s *execSchema) exitCode(p graphql.ResolveParams) (any, error) {
	parent := router.Parent[*Exec](p.Source)
	output, err := parent.Val.Metadata.ReadFile(p.Context, s.gw, "/exitCode")
	if err != nil {
		return nil, err
	}

	i, err := strconv.Atoi(string(output))
	if err != nil {
		return nil, err
	}
	return router.WithVal(parent, i), nil
}

func (s *execSchema) mount(p graphql.ResolveParams) (any, error) {
	parent := router.Parent[*Exec](p.Source)
	path := p.Args["path"].(string)
	path = filepath.Clean(path)

	mnt, ok := parent.Val.Mounts[path]
	if !ok {
		return nil, fmt.Errorf("missing mount path")
	}
	return router.WithVal(parent, mnt), nil
}
