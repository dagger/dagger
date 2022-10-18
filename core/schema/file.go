package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
)

type fileSchema struct {
	*baseSchema
}

var _ router.ExecutableSchema = &fileSchema{}

func (s *fileSchema) Name() string {
	return "file"
}

func (s *fileSchema) Schema() string {
	return File
}

var fileIDResolver = stringResolver(core.FileID(""))

func (s *fileSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"FileID": fileIDResolver,
		"Query": router.ObjectResolver{
			"file": router.ToResolver(s.file),
		},
		"File": router.ObjectResolver{
			"contents": router.ToResolver(s.contents),
			"secret":   router.ToResolver(s.secret),
			"size":     router.ToResolver(s.size),
		},
	}
}

func (s *fileSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type fileArgs struct {
	ID core.FileID
}

func (s *fileSchema) file(ctx *router.Context, parent any, args fileArgs) (*core.File, error) {
	return &core.File{
		ID: args.ID,
	}, nil
}

func (s *fileSchema) contents(ctx *router.Context, file *core.File, args any) (string, error) {
	content, err := file.Contents(ctx, s.gw)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

func (s *fileSchema) secret(ctx *router.Context, file *core.File, args any) (*core.Secret, error) {
	return file.Secret(ctx)
}

func (s *fileSchema) size(ctx *router.Context, file *core.File, args any) (int64, error) {
	info, err := file.Stat(ctx, s.gw)
	if err != nil {
		return 0, err
	}

	return info.Size_, nil
}
