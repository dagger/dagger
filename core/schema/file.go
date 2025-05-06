package schema

import (
	"context"
	"fmt"
	"io/fs"
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
			Doc(`Creates a file with the specified contents.`).
			Args(
				dagql.Arg("name").Doc(`Name of the new file. Example: "foo.txt"`),
				dagql.Arg("contents").Doc(`Contents of the new file. Example: "Hello world!"`),
				dagql.Arg("permissions").Doc(`Permissions of the new file. Example: 0600`),
			),
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
		dagql.Func("digest", s.digest).
			Doc(
				`Return the file's digest.
				The format of the digest is not guaranteed to be stable between releases of Dagger.
				It is guaranteed to be stable between invocations of the same Dagger engine.`,
			).
			Args(
				dagql.Arg("excludeMetadata").Doc(`If true, exclude metadata from the digest.`),
			),
		dagql.Func("withName", s.withName).
			Doc(`Retrieves this file with its name set to the given name.`).
			Args(
				dagql.Arg("name").Doc(`Name to set file to.`),
			),
		dagql.Func("export", s.export).
			View(AllVersion).
			DoNotCache("Writes to the local host.").
			Doc(`Writes the file to a file path on the host.`).
			Args(
				dagql.Arg("path").Doc(`Location of the written directory (e.g., "output.txt").`),
				dagql.Arg("allowParentDirPath").Doc(
					`If allowParentDirPath is true, the path argument can be a directory
				path, in which case the file will be created in that directory.`),
			),
		dagql.Func("export", s.exportLegacy).
			View(BeforeVersion("v0.12.0")).
			Extend(),
		dagql.Func("withTimestamps", s.withTimestamps).
			Doc(`Retrieves this file with its created/modified timestamps set to the given time.`).
			Args(
				dagql.Arg("timestamp").Doc(`Timestamp to set dir/files in.`,
					`Formatted in seconds following Unix epoch (e.g., 1672531199).`),
			),
	}.Install(s.srv)
}

func (s *fileSchema) file(ctx context.Context, parent *core.Query, args struct {
	Name        string
	Contents    string
	Permissions int `default:"0644"`
}) (*core.File, error) {
	return core.NewFileWithContents(ctx, parent, args.Name, []byte(args.Contents), fs.FileMode(args.Permissions), nil, parent.Platform())
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

type fileDigestArgs struct {
	ExcludeMetadata bool `default:"false"`
}

func (s *fileSchema) digest(ctx context.Context, file *core.File, args fileDigestArgs) (dagql.String, error) {
	digest, err := file.Digest(ctx, args.ExcludeMetadata)
	if err != nil {
		return "", err
	}

	return dagql.NewString(digest), nil
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

func (s *fileSchema) exportLegacy(ctx context.Context, parent *core.File, args fileExportArgs) (dagql.Boolean, error) {
	_, err := s.export(ctx, parent, args)
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
