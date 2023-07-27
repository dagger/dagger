package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
)

type fileSchema struct {
	*baseSchema

	host *core.Host
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
		"File": router.ToIDableObjectResolver(core.FileID.ToFile, router.ObjectResolver{
			"id":             router.ToResolver(s.id),
			"sync":           router.ToResolver(s.sync),
			"contents":       router.ToResolver(s.contents),
			"size":           router.ToResolver(s.size),
			"export":         router.ToResolver(s.export),
			"withTimestamps": router.ToResolver(s.withTimestamps),
		}),
	}
}

func (s *fileSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type fileArgs struct {
	ID core.FileID
}

func (s *fileSchema) file(ctx *router.Context, parent any, args fileArgs) (*core.File, error) {
	return args.ID.ToFile()
}

func (s *fileSchema) id(ctx *router.Context, parent *core.File, args any) (core.FileID, error) {
	return parent.ID()
}

func (s *fileSchema) sync(ctx *router.Context, parent *core.File, _ any) (core.FileID, error) {
	err := parent.Evaluate(ctx.Context, s.gw)
	if err != nil {
		return "", err
	}
	return parent.ID()
}

func (s *fileSchema) contents(ctx *router.Context, file *core.File, args any) (string, error) {
	content, err := file.Contents(ctx, s.gw)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

func (s *fileSchema) size(ctx *router.Context, file *core.File, args any) (int64, error) {
	info, err := file.Stat(ctx, s.gw)
	if err != nil {
		return 0, err
	}

	return info.Size_, nil
}

type fileExportArgs struct {
	Path               string
	AllowParentDirPath bool
}

func (s *fileSchema) export(ctx *router.Context, parent *core.File, args fileExportArgs) (bool, error) {
	err := parent.Export(ctx, s.host, args.Path, args.AllowParentDirPath, s.bkClient, s.solveOpts, s.solveCh)
	if err != nil {
		return false, err
	}

	return true, nil
}

type fileWithTimestampsArgs struct {
	Timestamp int
}

func (s *fileSchema) withTimestamps(ctx *router.Context, parent *core.File, args fileWithTimestampsArgs) (*core.File, error) {
	return parent.WithTimestamps(ctx, args.Timestamp)
}
