package schema

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type fileSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &fileSchema{}

func (s *fileSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("file", s.file).
			Deprecated("Use `loadFileFromID` instead."),
	}.Install(s.srv)

	dagql.Fields[*core.File]{
		Syncer[*core.File]().
			Doc(`Force evaluation in the engine.`),
		dagql.Func("contents", s.contents).
			Doc(`Retrieves the contents of the file.`),
		dagql.Func("size", s.size).
			Doc(`Retrieves the size of the file, in bytes.`),
		dagql.Func("name", s.name).
			Doc(`Retrieves the name of the file.`),
		dagql.Func("withName", s.withName).
			Doc(`Retrieves this file with its name set to the given name.`).
			ArgDoc("name", `Name to set file to.`),
		dagql.Func("export", s.export).
			Impure("Writes to the local host.").
			Doc(`Writes the file to a file path on the host.`).
			ArgDoc("path", `Location of the written directory (e.g., "output.txt").`).
			ArgDoc("allowParentDirPath",
				`If allowParentDirPath is true, the path argument can be a directory
				path, in which case the file will be created in that directory.`),
		dagql.Func("withTimestamps", s.withTimestamps).
			Doc(`Retrieves this file with its created/modified timestamps set to the given time.`).
			ArgDoc("timestamp", `Timestamp to set dir/files in.`,
				`Formatted in seconds following Unix epoch (e.g., 1672531199).`),
	}.Install(s.srv)
}

type fileArgs struct {
	ID core.FileID
}

func (s *fileSchema) file(ctx context.Context, parent *core.Query, args fileArgs) (*core.File, error) {
	val, err := args.ID.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return val.Self, nil
}

func (s *fileSchema) contents(ctx context.Context, file *core.File, args struct{}) (dagql.String, error) {
	content, err := file.Contents(ctx)
	if err != nil {
		return "", err
	}

	return dagql.NewString(string(content)), nil
}

func (s *fileSchema) size(ctx context.Context, file *core.File, args struct{}) (dagql.Int, error) {
	info, err := file.Stat(ctx)
	if err != nil {
		return 0, err
	}

	return dagql.NewInt(int(info.Size_)), nil
}

func (s *fileSchema) name(ctx context.Context, file *core.File, args struct{}) (dagql.String, error) {
	return dagql.NewString(filepath.Base(file.File)), nil
}

type fileWithNameArgs struct {
	Name string
}

func (s *fileSchema) withName(ctx context.Context, parent *core.File, args fileWithNameArgs) (*core.File, error) {
	return parent.WithName(ctx, args.Name)
}

type fileExportArgs struct {
	Path               string
	AllowParentDirPath bool `default:"false"`
}

func (s *fileSchema) export(ctx context.Context, parent *core.File, args fileExportArgs) (dagql.String, error) {
	err := parent.Export(ctx, args.Path, args.AllowParentDirPath)
	if err != nil {
		return "", err
	}
	bk, err := parent.Query.Buildkit(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get buildkit client: %w", err)
	}
	stat, err := bk.StatCallerHostPath(ctx, args.Path, true)
	if err != nil {
		return "", err
	}
	return dagql.String(stat.Path), err
}

type fileWithTimestampsArgs struct {
	Timestamp int
}

func (s *fileSchema) withTimestamps(ctx context.Context, parent *core.File, args fileWithTimestampsArgs) (*core.File, error) {
	return parent.WithTimestamps(ctx, args.Timestamp)
}
