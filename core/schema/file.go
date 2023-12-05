package schema

import (
	"context"

	"github.com/dagger/dagger/core"
)

type fileSchema struct {
	*APIServer

	host *core.Host
	svcs *core.Services
}

var _ SchemaResolvers = &fileSchema{}

func (s *fileSchema) Name() string {
	return "file"
}

func (s *fileSchema) Schema() string {
	return File
}

func (s *fileSchema) Resolvers() Resolvers {
	rs := Resolvers{
		"Query": ObjectResolver{
			"file": ToResolver(s.file),
		},
	}

	ResolveIDable[core.File](rs, "File", ObjectResolver{
		"sync":           ToResolver(s.sync),
		"contents":       ToResolver(s.contents),
		"size":           ToResolver(s.size),
		"export":         ToResolver(s.export),
		"withTimestamps": ToResolver(s.withTimestamps),
	})

	return rs
}

type fileArgs struct {
	ID core.FileID
}

func (s *fileSchema) file(ctx context.Context, parent any, args fileArgs) (*core.File, error) {
	return args.ID.Decode()
}

func (s *fileSchema) sync(ctx context.Context, parent *core.File, _ any) (core.FileID, error) {
	err := parent.Evaluate(ctx, s.bk, s.svcs)
	if err != nil {
		return "", err
	}
	return parent.ID()
}

func (s *fileSchema) contents(ctx context.Context, file *core.File, args any) (string, error) {
	content, err := file.Contents(ctx, s.bk, s.svcs)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

func (s *fileSchema) size(ctx context.Context, file *core.File, args any) (int64, error) {
	info, err := file.Stat(ctx, s.bk, s.svcs)
	if err != nil {
		return 0, err
	}

	return info.Size_, nil
}

type fileExportArgs struct {
	Path               string
	AllowParentDirPath bool
}

func (s *fileSchema) export(ctx context.Context, parent *core.File, args fileExportArgs) (bool, error) {
	err := parent.Export(ctx, s.bk, s.host, s.svcs, args.Path, args.AllowParentDirPath)
	if err != nil {
		return false, err
	}

	return true, nil
}

type fileWithTimestampsArgs struct {
	Timestamp int
}

func (s *fileSchema) withTimestamps(ctx context.Context, parent *core.File, args fileWithTimestampsArgs) (*core.File, error) {
	return parent.WithTimestamps(ctx, args.Timestamp)
}
