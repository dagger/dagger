package schema

import (
	"fmt"
	"os"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
)

type hostSchema struct {
	*baseSchema
	workdirID core.HostDirectoryID
}

var _ router.ExecutableSchema = &hostSchema{}

func (s *hostSchema) Name() string {
	return "host"
}

func (s *hostSchema) Schema() string {
	return Host
}

func (s *hostSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"HostDirectoryID": stringResolver(core.HostDirectoryID("")),
		"Query": router.ObjectResolver{
			"host": router.PassthroughResolver,
		},
		"Host": router.ObjectResolver{
			"workdir":     router.ToResolver(s.workdir),
			"directory":   router.ToResolver(s.directory),
			"envVariable": router.ToResolver(s.envVariable),
		},
		"HostDirectory": router.ObjectResolver{
			"read":  router.ToResolver(s.dirRead),
			"write": router.ToResolver(s.dirWrite),
		},
		"HostVariable": router.ObjectResolver{
			"value":  router.ToResolver(s.envVariableValue),
			"secret": router.ToResolver(s.envVariableSecret),
		},
	}
}

func (s *hostSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

func (s *hostSchema) workdir(ctx *router.Context, parent any, args any) (*core.HostDirectory, error) {
	return &core.HostDirectory{
		ID: s.workdirID,
	}, nil
}

type hostVariableArgs struct {
	Name string
}

func (s *hostSchema) envVariable(ctx *router.Context, parent any, args hostVariableArgs) (*core.HostVariable, error) {
	return &core.HostVariable{
		Name: args.Name,
	}, nil
}

func (s *hostSchema) envVariableValue(ctx *router.Context, parent *core.HostVariable, args any) (string, error) {
	return os.Getenv(parent.Name), nil
}

func (s *hostSchema) envVariableSecret(ctx *router.Context, parent *core.HostVariable, args any) (*core.Secret, error) {
	return core.NewSecretFromHostEnv(parent.Name)
}

type hostDirectoryArgs struct {
	ID core.HostDirectoryID
}

func (s *hostSchema) directory(ctx *router.Context, parent any, args hostDirectoryArgs) (*core.HostDirectory, error) {
	return &core.HostDirectory{
		ID: args.ID,
	}, nil
}

func (s *hostSchema) dirRead(ctx *router.Context, parent *core.HostDirectory, args any) (*core.Directory, error) {
	return parent.Read(ctx, s.platform)
}

type hostDirectoryWriteArgs struct {
	Contents core.DirectoryID
	Path     string
}

func (s *hostSchema) dirWrite(ctx *router.Context, parent *core.HostDirectory, args hostDirectoryWriteArgs) (bool, error) {
	dir, found := s.solveOpts.LocalDirs[string(parent.ID)]
	if !found {
		return false, fmt.Errorf("unknown host directory %q", parent.ID)
	}

	return parent.Write(ctx,
		dir, args.Path,
		&core.Directory{ID: args.Contents},
		s.bkClient, s.solveOpts, s.solveCh,
	)
}
