package schema

import (
	"os"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
)

type hostSchema struct {
	*baseSchema

	host *core.Host
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
			"workdir":   router.ToResolver(s.workdir),
			"directory": router.ToResolver(s.directory),
			"variable":  router.ToResolver(s.variable),
		},
		"HostVariable": router.ObjectResolver{
			"value":  router.ToResolver(s.variableValue),
			"secret": router.ToResolver(s.variableSecret),
		},
	}
}

func (s *hostSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

func (s *hostSchema) workdir(ctx *router.Context, parent any, args any) (*core.Directory, error) {
	return s.host.Directory(ctx, ".", s.platform)
}

type hostVariableArgs struct {
	Name string
}

func (s *hostSchema) variable(ctx *router.Context, parent any, args hostVariableArgs) (*core.HostVariable, error) {
	return &core.HostVariable{
		Name: args.Name,
	}, nil
}

func (s *hostSchema) variableValue(ctx *router.Context, parent *core.HostVariable, args any) (string, error) {
	return os.Getenv(parent.Name), nil
}

func (s *hostSchema) variableSecret(ctx *router.Context, parent *core.HostVariable, args any) (*core.Secret, error) {
	return core.NewSecretFromHostEnv(parent.Name)
}

type hostDirectoryArgs struct {
	Path string
}

func (s *hostSchema) directory(ctx *router.Context, parent any, args hostDirectoryArgs) (*core.Directory, error) {
	return s.host.Directory(ctx, args.Path, s.platform)
}
