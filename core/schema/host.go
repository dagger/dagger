package schema

import (
	"fmt"
	"os"
	"path/filepath"

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
			"directory":     router.ToResolver(s.directory),
			"file":          router.ToResolver(s.file),
			"unixSocket":    router.ToResolver(s.socket),
			"setSecretFile": router.ToResolver(s.setSecretFile),
		},
	}
}

func (s *hostSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type setSecretFileArgs struct {
	Name string
	Path string
}

func (s *hostSchema) setSecretFile(ctx *router.Context, _ any, args setSecretFileArgs) (*core.Secret, error) {
	if s.host.DisableRW {
		return nil, core.ErrHostRWDisabled
	}

	var absPath string
	var err error
	if filepath.IsAbs(args.Path) {
		absPath = args.Path
	} else {
		absPath = filepath.Join(s.host.Workdir, args.Path)
	}

	absPath, err = filepath.EvalSymlinks(absPath)
	if err != nil {
		return nil, fmt.Errorf("eval symlinks: %w", err)
	}

	secretFileContent, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	secretID, err := s.secrets.AddSecret(ctx, args.Name, secretFileContent)
	if err != nil {
		return nil, err
	}

	return secretID.ToSecret()
}

type hostDirectoryArgs struct {
	Path string

	core.CopyFilter
}

func (s *hostSchema) directory(ctx *router.Context, parent *core.Query, args hostDirectoryArgs) (*core.Directory, error) {
	return s.host.Directory(ctx, s.gw, args.Path, parent.PipelinePath(), "host.directory", s.platform, args.CopyFilter)
}

type hostSocketArgs struct {
	Path string
}

func (s *hostSchema) socket(ctx *router.Context, _ any, args hostSocketArgs) (*core.Socket, error) {
	return s.host.Socket(ctx, args.Path)
}

type hostFileArgs struct {
	Path string
}

func (s *hostSchema) file(ctx *router.Context, parent *core.Query, args hostFileArgs) (*core.File, error) {
	return s.host.File(ctx, s.gw, args.Path, parent.PipelinePath(), s.platform)
}
