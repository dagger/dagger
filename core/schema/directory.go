package schema

import (
	"go.dagger.io/dagger/core"
	"go.dagger.io/dagger/router"
)

type directorySchema struct {
	*baseSchema
}

var _ router.ExecutableSchema = &directorySchema{}

func (s *directorySchema) Name() string {
	return "directory"
}

func (s *directorySchema) Schema() string {
	return Directory
}

var directoryIDResolver = stringResolver(core.DirectoryID(""))

func (s *directorySchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"DirectoryID": directoryIDResolver,
		"Query": router.ObjectResolver{
			"directory": router.ToResolver(s.directory),
		},
		"Directory": router.ObjectResolver{
			"contents":         router.ToResolver(s.contents),
			"file":             router.ToResolver(s.file),
			"secret":           router.ErrResolver(ErrNotImplementedYet),
			"withNewFile":      router.ToResolver(s.withNewFile),
			"withCopiedFile":   router.ToResolver(s.withCopiedFile),
			"withoutFile":      router.ErrResolver(ErrNotImplementedYet),
			"directory":        router.ToResolver(s.subdirectory),
			"withDirectory":    router.ToResolver(s.withDirectory),
			"withoutDirectory": router.ErrResolver(ErrNotImplementedYet),
			"diff":             router.ErrResolver(ErrNotImplementedYet),
		},
	}
}

func (s *directorySchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type directoryArgs struct {
	ID core.DirectoryID
}

func (s *directorySchema) directory(ctx *router.Context, parent any, args directoryArgs) (*core.Directory, error) {
	return &core.Directory{
		ID: args.ID,
	}, nil
}

type subdirectoryArgs struct {
	Path string
}

func (s *directorySchema) subdirectory(ctx *router.Context, parent *core.Directory, args subdirectoryArgs) (*core.Directory, error) {
	return parent.Directory(ctx, args.Path)
}

type withDirectoryArgs struct {
	Path      string
	Directory core.DirectoryID
}

func (s *directorySchema) withDirectory(ctx *router.Context, parent *core.Directory, args withDirectoryArgs) (*core.Directory, error) {
	return parent.WithDirectory(ctx, args.Path, &core.Directory{ID: args.Directory})
}

type contentArgs struct {
	Path string
}

func (s *directorySchema) contents(ctx *router.Context, parent *core.Directory, args contentArgs) ([]string, error) {
	return parent.Contents(ctx, s.gw, args.Path)
}

type dirFileArgs struct {
	Path string
}

func (s *directorySchema) file(ctx *router.Context, parent *core.Directory, args dirFileArgs) (*core.File, error) {
	return parent.File(ctx, args.Path)
}

type withNewFileArgs struct {
	Path     string
	Contents string
}

func (s *directorySchema) withNewFile(ctx *router.Context, parent *core.Directory, args withNewFileArgs) (*core.Directory, error) {
	return parent.WithNewFile(ctx, s.gw, args.Path, []byte(args.Contents))
}

type withCopiedFileArgs struct {
	Path   string
	Source core.FileID
}

func (s *directorySchema) withCopiedFile(ctx *router.Context, parent *core.Directory, args withCopiedFileArgs) (*core.Directory, error) {
	return parent.WithCopiedFile(ctx, args.Path, &core.File{ID: args.Source})
}
