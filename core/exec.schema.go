package core

import (
	"fmt"
	"strconv"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/router"
	"github.com/dagger/cloak/shim"
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

func (r *execSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Filesystem": router.ObjectResolver{
			"exec": r.Exec,
		},
		"Exec": router.ObjectResolver{
			"stdout":   r.Stdout,
			"stderr":   r.Stderr,
			"exitCode": r.ExitCode,
			"mount":    r.Mount,
		},
	}
}

func (r *execSchema) Exec(p graphql.ResolveParams) (any, error) {
	obj, err := filesystem.FromSource(p.Source)
	if err != nil {
		return nil, err
	}

	var input ExecInput
	if err := convertArg(p.Args["input"], &input); err != nil {
		return nil, err
	}

	shim, err := shim.Build(p.Context, r.gw, r.platform)
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

	metadataFS, err := filesystem.FromState(p.Context, execState.GetMount("/dagger"), r.platform)
	if err != nil {
		return nil, err
	}

	mounts := map[string]*filesystem.Filesystem{}
	for _, mount := range input.Mounts {
		mountFS, err := filesystem.FromState(p.Context, execState.GetMount(mount.Path), r.platform)
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

func (r *execSchema) Stdout(p graphql.ResolveParams) (any, error) {
	obj := p.Source.(*Exec)
	output, err := obj.Metadata.ReadFile(p.Context, r.gw, "/stdout")
	if err != nil {
		return nil, err
	}

	return truncate(string(output), p.Args), nil
}

func (r *execSchema) Stderr(p graphql.ResolveParams) (any, error) {
	obj := p.Source.(*Exec)
	output, err := obj.Metadata.ReadFile(p.Context, r.gw, "/stderr")
	if err != nil {
		return nil, err
	}

	return truncate(string(output), p.Args), nil
}

func (r *execSchema) ExitCode(p graphql.ResolveParams) (any, error) {
	obj := p.Source.(*Exec)
	output, err := obj.Metadata.ReadFile(p.Context, r.gw, "/exitCode")
	if err != nil {
		return nil, err
	}

	return strconv.Atoi(string(output))
}

func (r *execSchema) Mount(p graphql.ResolveParams) (any, error) {
	obj := p.Source.(*Exec)
	path := p.Args["path"].(string)

	mnt, ok := obj.Mounts[path]
	if !ok {
		return nil, fmt.Errorf("missing mount path")
	}
	return mnt, nil
}
