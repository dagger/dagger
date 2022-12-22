package schema

import (
	"io/fs"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type directorySchema struct {
	*baseSchema

	host *core.Host
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
			"entries":          router.ToResolver(s.entries),
			"file":             router.ToResolver(s.file),
			"withFile":         router.ToResolver(s.withFile),
			"withNewFile":      router.ToResolver(s.withNewFile),
			"withoutFile":      router.ToResolver(s.withoutFile),
			"directory":        router.ToResolver(s.subdirectory),
			"withDirectory":    router.ToResolver(s.withDirectory),
			"withTimestamps":   router.ToResolver(s.withTimestamps),
			"withNewDirectory": router.ToResolver(s.withNewDirectory),
			"withoutDirectory": router.ToResolver(s.withoutDirectory),
			"diff":             router.ToResolver(s.diff),
			"export":           router.ToResolver(s.export),
			"dockerBuild":      router.ToResolver(s.dockerBuild),
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

type withNewDirectoryArgs struct {
	Path        string
	Permissions fs.FileMode
}

func (s *directorySchema) withNewDirectory(ctx *router.Context, parent *core.Directory, args withNewDirectoryArgs) (*core.Directory, error) {
	return parent.WithNewDirectory(ctx, s.gw, args.Path, args.Permissions)
}

type withDirectoryArgs struct {
	Path      string
	Directory core.DirectoryID

	core.CopyFilter
}

func (s *directorySchema) withDirectory(ctx *router.Context, parent *core.Directory, args withDirectoryArgs) (*core.Directory, error) {
	return parent.WithDirectory(ctx, args.Path, &core.Directory{ID: args.Directory}, args.CopyFilter)
}

type dirWithTimestampsArgs struct {
	Timestamp int
}

func (s *directorySchema) withTimestamps(ctx *router.Context, parent *core.Directory, args dirWithTimestampsArgs) (*core.Directory, error) {
	return parent.WithTimestamps(ctx, args.Timestamp)
}

type entriesArgs struct {
	Path string
}

func (s *directorySchema) entries(ctx *router.Context, parent *core.Directory, args entriesArgs) ([]string, error) {
	return parent.Entries(ctx, s.gw, args.Path)
}

type dirFileArgs struct {
	Path string
}

func (s *directorySchema) file(ctx *router.Context, parent *core.Directory, args dirFileArgs) (*core.File, error) {
	return parent.File(ctx, args.Path)
}

type withNewFileArgs struct {
	Path        string
	Contents    string
	Permissions fs.FileMode
}

func (s *directorySchema) withNewFile(ctx *router.Context, parent *core.Directory, args withNewFileArgs) (*core.Directory, error) {
	return parent.WithNewFile(ctx, s.gw, args.Path, []byte(args.Contents), args.Permissions)
}

type withFileArgs struct {
	Path        string
	Source      core.FileID
	Permissions fs.FileMode
}

func (s *directorySchema) withFile(ctx *router.Context, parent *core.Directory, args withFileArgs) (*core.Directory, error) {
	return parent.WithFile(ctx, args.Path, &core.File{ID: args.Source}, args.Permissions)
}

type withoutDirectoryArgs struct {
	Path string
}

func (s *directorySchema) withoutDirectory(ctx *router.Context, parent *core.Directory, args withoutDirectoryArgs) (*core.Directory, error) {
	return parent.Without(ctx, args.Path)
}

type withoutFileArgs struct {
	Path string
}

func (s *directorySchema) withoutFile(ctx *router.Context, parent *core.Directory, args withoutFileArgs) (*core.Directory, error) {
	return parent.Without(ctx, args.Path)
}

type diffArgs struct {
	Other core.DirectoryID
}

func (s *directorySchema) diff(ctx *router.Context, parent *core.Directory, args diffArgs) (*core.Directory, error) {
	return parent.Diff(ctx, &core.Directory{ID: args.Other})
}

type dirExportArgs struct {
	Path string
}

func (s *directorySchema) export(ctx *router.Context, parent *core.Directory, args dirExportArgs) (bool, error) {
	err := parent.Export(ctx, s.host, args.Path, s.bkClient, s.solveOpts, s.solveCh)
	if err != nil {
		return false, err
	}

	return true, nil
}

type dirDockerBuildArgs struct {
	Platform   *specs.Platform
	Dockerfile string
}

func (s *directorySchema) dockerBuild(ctx *router.Context, parent *core.Directory, args dirDockerBuildArgs) (*core.Container, error) {
	platform := s.baseSchema.platform
	if args.Platform != nil {
		platform = *args.Platform
	}
	ctr, err := core.NewContainer("", platform)
	if err != nil {
		return ctr, err
	}
	return ctr.Build(ctx, s.gw, parent, args.Dockerfile)
}
