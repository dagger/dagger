package schema

import (
	"os"

	"github.com/dagger/dagger/core"
)

type hostSchema struct {
	*MergedSchemas

	host *core.Host
}

var _ ExecutableSchema = &hostSchema{}

func (s *hostSchema) Name() string {
	return "host"
}

func (s *hostSchema) Schema() string {
	return Host
}

func (s *hostSchema) Resolvers() Resolvers {
	return Resolvers{
		"Query": ObjectResolver{
			"host": PassthroughResolver,
		},
		"Host": ObjectResolver{
			"directory":   ToResolver(s.directory),
			"file":        ToResolver(s.file),
			"envVariable": ToResolver(s.envVariable),
			"unixSocket":  ToResolver(s.socket),
		},
		"HostVariable": ObjectResolver{
			"value":  ToResolver(s.envVariableValue),
			"secret": ToResolver(s.envVariableSecret),
		},
	}
}

func (s *hostSchema) Dependencies() []ExecutableSchema {
	return nil
}

type hostWorkdirArgs struct {
	core.CopyFilter
}

type hostVariableArgs struct {
	Name string
}

func (s *hostSchema) envVariable(ctx *core.Context, parent any, args hostVariableArgs) (*core.HostVariable, error) {
	return &core.HostVariable{
		Name: args.Name,
	}, nil
}

func (s *hostSchema) envVariableValue(ctx *core.Context, parent *core.HostVariable, args any) (string, error) {
	return os.Getenv(parent.Name), nil
}

func (s *hostSchema) envVariableSecret(ctx *core.Context, parent *core.HostVariable, args any) (*core.Secret, error) {
	return core.NewSecretFromHostEnv(parent.Name), nil
}

type hostDirectoryArgs struct {
	Path string

	core.CopyFilter
}

func (s *hostSchema) directory(ctx *core.Context, parent *core.Query, args hostDirectoryArgs) (*core.Directory, error) {
	return s.host.Directory(ctx, s.bk, args.Path, parent.PipelinePath(), "host.directory", s.platform, args.CopyFilter)
}

type hostSocketArgs struct {
	Path string
}

func (s *hostSchema) socket(ctx *core.Context, parent any, args hostSocketArgs) (*core.Socket, error) {
	return s.host.Socket(ctx, args.Path)
}

type hostFileArgs struct {
	Path string
}

func (s *hostSchema) file(ctx *core.Context, parent *core.Query, args hostFileArgs) (*core.File, error) {
	return s.host.File(ctx, s.bk, args.Path, parent.PipelinePath(), s.platform)
}
