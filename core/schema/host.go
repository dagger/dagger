package schema

import (
	"os"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
	"github.com/moby/buildkit/client/llb"
)

type hostSchema struct {
	*baseSchema
	workdirPath string
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
	return s.directory(ctx, parent, hostDirectoryArgs{
		Path: s.workdirPath,
	})
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
	// copy to scratch to avoid making buildkit's snapshot of the local dir immutable,
	// which makes it unable to reused, which in turn creates cache invalidations
	// TODO: this should be optional, the above issue can also be avoided w/ readonly
	// mount when possible
	// st := llb.Scratch().File(llb.Copy(llb.Local(
	// 	id,
	// 	// TODO: better shared key hint?
	// 	llb.SharedKeyHint(id),
	// 	// FIXME: should not be hardcoded
	// 	llb.ExcludePatterns([]string{"**/node_modules"}),
	// ), "/", "/"))

	return core.NewDirectory(ctx, llb.Local(args.Path), "", s.platform, map[string]string{
		// TODO: hash ID?
		// TODO: validate relative paths?
		args.Path: args.Path,
	})
}
