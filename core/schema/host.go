package schema

import (
	"os"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
)

type hostSchema struct {
	*baseSchema
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
		"Query": router.ObjectResolver{
			"host": router.PassthroughResolver,
		},
		"Host": router.ObjectResolver{
			"workdir":     router.ToResolver(s.workdir),
			"directory":   router.ToResolver(s.directory),
			"envVariable": router.ToResolver(s.envVariable),
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

type hostWorkdirArgs struct {
	core.CopyFilter
}

func (s *hostSchema) workdir(ctx *router.Context, parent any, args hostWorkdirArgs) (*core.Directory, error) {
	host, err := s.sessions.Host(ctx, ctx.SessionID)
	if err != nil {
		return nil, err
	}

	return host.Directory(ctx, ".", s.platform, args.CopyFilter)
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
	Path string

	core.CopyFilter
}

func (s *hostSchema) directory(ctx *router.Context, parent any, args hostDirectoryArgs) (*core.Directory, error) {
	host, err := s.sessions.Host(ctx, ctx.SessionID)
	if err != nil {
		return nil, err
	}

	return host.Directory(ctx, args.Path, s.platform, args.CopyFilter)
}
