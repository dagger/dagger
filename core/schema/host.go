package schema

import (
	"os"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
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
			"workdir":     router.ToResolver(s.workdir),
			"directory":   router.ToResolver(s.directory),
			"envVariable": router.ToResolver(s.envVariable),
			"unixSocket":  router.ToResolver(s.socket),
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

func (s *hostSchema) workdir(ctx *router.Context, parent *core.Query, args hostWorkdirArgs) (*core.Directory, error) {
	pipeline := pipeline.Path{}
	if parent != nil {
		pipeline = parent.Context.Pipeline
	}
	return s.host.Directory(ctx, ".", pipeline, s.platform, args.CopyFilter)
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

func (s *hostSchema) directory(ctx *router.Context, parent *core.Query, args hostDirectoryArgs) (*core.Directory, error) {
	pipeline := pipeline.Path{}
	if parent != nil {
		pipeline = parent.Context.Pipeline
	}
	return s.host.Directory(ctx, args.Path, pipeline, s.platform, args.CopyFilter)
}

type hostSocketArgs struct {
	Path string
}

func (s *hostSchema) socket(ctx *router.Context, parent any, args hostSocketArgs) (*core.Socket, error) {
	return s.host.Socket(ctx, args.Path)
}
