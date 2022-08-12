package core

import (
	"fmt"
	"strconv"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/core/shim"
	"github.com/dagger/cloak/router"
	"github.com/graphql-go/graphql"
	"github.com/moby/buildkit/client/llb"
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

type ExecInput struct {
	Args    []string
	Mounts  []MountInput
	Workdir string
}

var _ router.ExecutableSchema = &filesystemSchema{}

type execSchema struct {
	*baseSchema
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

	input ExecInput {
		"""
		Command to execute
		Example: ["echo", "hello, world!"]
		"""
		args: [String!]!

		"Transient filesystem mounts"
		mounts: [MountInput!]

		"Working directory"
		workdir: String
	}

	# FIXME: broken
	# extend type Filesystem {
	#	"execute a command inside this filesystem"
	# 	exec(input: ExecInput!): Exec!
	# }
	`
}

func (s *execSchema) Operations() string {
	return `
	query Exec($fsid: FSID!, $input: ExecInput!) {
		core {
			filesystem(id: $fsid) {
				exec(input: $input) {
					fs {
						id
					}
				}
			}
		}
	}
	query ExecGetMount($fsid: FSID!, $input: ExecInput!, $getPath: String!) {
		core {
			filesystem(id: $fsid) {
				exec(input: $input) {
					mount(path: $getPath) {
						id
					}
				}
			}
		}
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

func (s *execSchema) exec(p graphql.ResolveParams) (any, error) {
	obj, err := filesystem.FromSource(p.Source)
	if err != nil {
		return nil, err
	}

	var input ExecInput
	if err := convertArg(p.Args["input"], &input); err != nil {
		return nil, err
	}

	shim, err := shim.Build(p.Context, s.gw, s.platform)
	if err != nil {
		return nil, err
	}

	runOpt := []llb.RunOption{
		llb.Args(append([]string{"/_shim"}, input.Args...)),
		llb.AddMount("/_shim", shim, llb.SourcePath("/_shim")),
		llb.Dir(input.Workdir),
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

	fs, err := s.Solve(p.Context, execState.Root())
	if err != nil {
		return nil, err
	}

	metadataFS, err := filesystem.FromState(p.Context, execState.GetMount("/dagger"), s.platform)
	if err != nil {
		return nil, err
	}

	mounts := map[string]*filesystem.Filesystem{}
	for _, mount := range input.Mounts {
		mountFS, err := filesystem.FromState(p.Context, execState.GetMount(mount.Path), s.platform)
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

func (s *execSchema) stdout(p graphql.ResolveParams) (any, error) {
	obj := p.Source.(*Exec)
	output, err := obj.Metadata.ReadFile(p.Context, s.gw, "/stdout")
	if err != nil {
		return nil, err
	}

	return truncate(string(output), p.Args), nil
}

func (s *execSchema) stderr(p graphql.ResolveParams) (any, error) {
	obj := p.Source.(*Exec)
	output, err := obj.Metadata.ReadFile(p.Context, s.gw, "/stderr")
	if err != nil {
		return nil, err
	}

	return truncate(string(output), p.Args), nil
}

func (s *execSchema) exitCode(p graphql.ResolveParams) (any, error) {
	obj := p.Source.(*Exec)
	output, err := obj.Metadata.ReadFile(p.Context, s.gw, "/exitCode")
	if err != nil {
		return nil, err
	}

	return strconv.Atoi(string(output))
}

func (s *execSchema) mount(p graphql.ResolveParams) (any, error) {
	obj := p.Source.(*Exec)
	path := p.Args["path"].(string)

	mnt, ok := obj.Mounts[path]
	if !ok {
		return nil, fmt.Errorf("missing mount path")
	}
	return mnt, nil
}
