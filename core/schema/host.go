package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
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
			"directory":  ToResolver(s.directory),
			"file":       ToResolver(s.file),
			"unixSocket": ToResolver(s.socket),
		},
	}
}

func (s *hostSchema) Dependencies() []ExecutableSchema {
	return nil
}

type setSecretFileArgs struct {
	Name string
	Path string
}

func (s *hostSchema) setSecretFile(ctx *core.Context, _ any, args setSecretFileArgs) (*core.Secret, error) {
	panic("re-incorporate")
	/* TODO:
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
	*/
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
	// TODO: enforcement that requester session is granted access to source session at this path
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.host.Socket(ctx, args.Path, clientMetadata.ClientHostname)
}

type hostFileArgs struct {
	Path string
}

func (s *hostSchema) file(ctx *core.Context, parent *core.Query, args hostFileArgs) (*core.File, error) {
	return s.host.File(ctx, s.bk, args.Path, parent.PipelinePath(), s.platform)
}
