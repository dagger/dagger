package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/resourceid"
)

type fileSchema struct {
	*MergedSchemas

	host *core.Host
	svcs *core.Services
}

var _ ExecutableSchema = &fileSchema{}

func (s *fileSchema) Name() string {
	return "file"
}

func (s *fileSchema) SourceModuleName() string {
	return coreModuleName
}

func (s *fileSchema) Schema() string {
	return File
}

func (s *fileSchema) Resolvers() Resolvers {
	rs := Resolvers{
		"Query": ObjectResolver{
			"file": ToCachedResolver(s.queryCache, s.file),
		},
	}

	ResolveIDable[*core.File](s.queryCache, s.MergedSchemas, rs, "File", ObjectResolver{
		"sync":           ToCachedResolver(s.queryCache, s.sync),
		"contents":       ToCachedResolver(s.queryCache, s.contents),
		"size":           ToCachedResolver(s.queryCache, s.size),
		"export":         ToResolver(s.export), // XXX(vito): test
		"withTimestamps": ToCachedResolver(s.queryCache, s.withTimestamps),
	})

	return rs
}

type fileArgs struct {
	ID core.FileID
}

func (s *fileSchema) file(ctx context.Context, parent *core.Query, args fileArgs) (*core.File, error) {
	return load(ctx, args.ID, s.MergedSchemas)
}

func (s *fileSchema) sync(ctx context.Context, parent *core.File, _ any) (core.FileID, error) {
	err := parent.Evaluate(ctx, s.bk, s.svcs)
	if err != nil {
		return nil, err
	}
	return resourceid.FromProto[*core.File](parent.ID()), nil
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
