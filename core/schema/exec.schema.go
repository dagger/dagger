package core

import (
	"fmt"
	"strconv"

	"github.com/dagger/cloak/core"
	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/core/shim"
	"github.com/dagger/cloak/router"
	"github.com/graphql-go/graphql"
	"github.com/moby/buildkit/client/llb"
)

func init() {
	core.Register("exec", func(base *core.BaseSchema) router.ExecutableSchema { return &execSchema{base} })
}

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
	*core.BaseSchema
}

func (s *execSchema) Schema() string {
	return `
	type Exec {
		fs: Filesystem!
		stdout(lines: Int): String
		stderr(lines: Int): String
		exitCode: Int
		mount(path: String!): Filesystem!
	}

	input MountInput {
		path: String!
		fs: FSID!
	}

	input ExecInput {
		args: [String!]!
		mounts: [MountInput!]
		workdir: String
	}

	# FIXME: broken
	# extend type Filesystem {
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

func (r *execSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Filesystem": router.ObjectResolver{
			"exec": r.exec,
		},
		"Exec": router.ObjectResolver{
			"stdout":   r.stdout,
			"stderr":   r.stderr,
			"exitCode": r.exitCode,
			"mount":    r.mount,
		},
	}
}

func (r *execSchema) exec(p graphql.ResolveParams) (any, error) {
	obj, err := filesystem.FromSource(p.Source)
	if err != nil {
		return nil, err
	}

	var input ExecInput
	if err := convertArg(p.Args["input"], &input); err != nil {
		return nil, err
	}

	shim, err := shim.Build(p.Context, r.Gateway, r.Platform)
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

	fs, err := r.Solve(p.Context, execState.Root())
	if err != nil {
		return nil, err
	}

	metadataFS, err := filesystem.FromState(p.Context, execState.GetMount("/dagger"), r.Platform)
	if err != nil {
		return nil, err
	}

	mounts := map[string]*filesystem.Filesystem{}
	for _, mount := range input.Mounts {
		mountFS, err := filesystem.FromState(p.Context, execState.GetMount(mount.Path), r.Platform)
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

func (r *execSchema) stdout(p graphql.ResolveParams) (any, error) {
	obj := p.Source.(*Exec)
	output, err := obj.Metadata.ReadFile(p.Context, r.Gateway, "/stdout")
	if err != nil {
		return nil, err
	}

	return truncate(string(output), p.Args), nil
}

func (r *execSchema) stderr(p graphql.ResolveParams) (any, error) {
	obj := p.Source.(*Exec)
	output, err := obj.Metadata.ReadFile(p.Context, r.Gateway, "/stderr")
	if err != nil {
		return nil, err
	}

	return truncate(string(output), p.Args), nil
}

func (r *execSchema) exitCode(p graphql.ResolveParams) (any, error) {
	obj := p.Source.(*Exec)
	output, err := obj.Metadata.ReadFile(p.Context, r.Gateway, "/exitCode")
	if err != nil {
		return nil, err
	}

	return strconv.Atoi(string(output))
}

func (r *execSchema) mount(p graphql.ResolveParams) (any, error) {
	obj := p.Source.(*Exec)
	path := p.Args["path"].(string)

	mnt, ok := obj.Mounts[path]
	if !ok {
		return nil, fmt.Errorf("missing mount path")
	}
	return mnt, nil
}
