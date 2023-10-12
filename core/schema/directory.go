package schema

import (
	"io/fs"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type directorySchema struct {
	*MergedSchemas

	host       *core.Host
	svcs       *core.Services
	buildCache *core.CacheMap[uint64, *core.Container]
}

var _ ExecutableSchema = &directorySchema{}

func (s *directorySchema) Name() string {
	return "directory"
}

func (s *directorySchema) Schema() string {
	return Directory
}

func (s *directorySchema) Resolvers() Resolvers {
	rs := Resolvers{
		"Query": ObjectResolver{
			"directory": ToResolver(s.directory),
		},
	}

	ResolveIDable[core.Directory](rs, "Directory", ObjectResolver{
		"sync":             ToResolver(s.sync),
		"pipeline":         ToResolver(s.pipeline),
		"entries":          ToResolver(s.entries),
		"file":             ToResolver(s.file),
		"withFile":         ToResolver(s.withFile),
		"withNewFile":      ToResolver(s.withNewFile),
		"withoutFile":      ToResolver(s.withoutFile),
		"directory":        ToResolver(s.subdirectory),
		"withDirectory":    ToResolver(s.withDirectory),
		"withTimestamps":   ToResolver(s.withTimestamps),
		"withNewDirectory": ToResolver(s.withNewDirectory),
		"withoutDirectory": ToResolver(s.withoutDirectory),
		"diff":             ToResolver(s.diff),
		"export":           ToResolver(s.export),
		"dockerBuild":      ToResolver(s.dockerBuild),
	})

	return rs
}

func (s *directorySchema) Dependencies() []ExecutableSchema {
	return nil
}

type directoryPipelineArgs struct {
	Name        string
	Description string
	Labels      []pipeline.Label
}

func (s *directorySchema) pipeline(ctx *core.Context, parent *core.Directory, args directoryPipelineArgs) (*core.Directory, error) {
	return parent.WithPipeline(ctx, args.Name, args.Description, args.Labels)
}

type directoryArgs struct {
	ID core.DirectoryID
}

func (s *directorySchema) directory(ctx *core.Context, parent *core.Query, args directoryArgs) (*core.Directory, error) {
	if args.ID != "" {
		return args.ID.Decode()
	}
	platform := s.platform
	return core.NewScratchDirectory(parent.PipelinePath(), platform), nil
}

func (s *directorySchema) sync(ctx *core.Context, parent *core.Directory, _ any) (core.DirectoryID, error) {
	_, err := parent.Evaluate(ctx.Context, s.bk, s.svcs)
	if err != nil {
		return "", err
	}
	return parent.ID()
}

type subdirectoryArgs struct {
	Path string
}

func (s *directorySchema) subdirectory(ctx *core.Context, parent *core.Directory, args subdirectoryArgs) (*core.Directory, error) {
	return parent.Directory(ctx, s.bk, s.svcs, args.Path)
}

type withNewDirectoryArgs struct {
	Path        string
	Permissions fs.FileMode
}

func (s *directorySchema) withNewDirectory(ctx *core.Context, parent *core.Directory, args withNewDirectoryArgs) (*core.Directory, error) {
	return parent.WithNewDirectory(ctx, args.Path, args.Permissions)
}

type withDirectoryArgs struct {
	Path      string
	Directory core.DirectoryID

	core.CopyFilter
}

func (s *directorySchema) withDirectory(ctx *core.Context, parent *core.Directory, args withDirectoryArgs) (*core.Directory, error) {
	dir, err := args.Directory.Decode()
	if err != nil {
		return nil, err
	}
	return parent.WithDirectory(ctx, args.Path, dir, args.CopyFilter, nil)
}

type dirWithTimestampsArgs struct {
	Timestamp int
}

func (s *directorySchema) withTimestamps(ctx *core.Context, parent *core.Directory, args dirWithTimestampsArgs) (*core.Directory, error) {
	return parent.WithTimestamps(ctx, args.Timestamp)
}

type entriesArgs struct {
	Path string
}

func (s *directorySchema) entries(ctx *core.Context, parent *core.Directory, args entriesArgs) ([]string, error) {
	return parent.Entries(ctx, s.bk, s.svcs, args.Path)
}

type dirFileArgs struct {
	Path string
}

func (s *directorySchema) file(ctx *core.Context, parent *core.Directory, args dirFileArgs) (_ *core.File, rerr error) {
	return parent.File(ctx, s.bk, s.svcs, args.Path)
}

type withNewFileArgs struct {
	Path        string
	Contents    string
	Permissions fs.FileMode
}

func (s *directorySchema) withNewFile(ctx *core.Context, parent *core.Directory, args withNewFileArgs) (*core.Directory, error) {
	return parent.WithNewFile(ctx, args.Path, []byte(args.Contents), args.Permissions, nil)
}

type withFileArgs struct {
	Path        string
	Source      core.FileID
	Permissions fs.FileMode
}

func (s *directorySchema) withFile(ctx *core.Context, parent *core.Directory, args withFileArgs) (*core.Directory, error) {
	file, err := args.Source.Decode()
	if err != nil {
		return nil, err
	}

	return parent.WithFile(ctx, args.Path, file, args.Permissions, nil)
}

type withoutDirectoryArgs struct {
	Path string
}

func (s *directorySchema) withoutDirectory(ctx *core.Context, parent *core.Directory, args withoutDirectoryArgs) (*core.Directory, error) {
	return parent.Without(ctx, args.Path)
}

type withoutFileArgs struct {
	Path string
}

func (s *directorySchema) withoutFile(ctx *core.Context, parent *core.Directory, args withoutFileArgs) (*core.Directory, error) {
	return parent.Without(ctx, args.Path)
}

type diffArgs struct {
	Other core.DirectoryID
}

func (s *directorySchema) diff(ctx *core.Context, parent *core.Directory, args diffArgs) (*core.Directory, error) {
	dir, err := args.Other.Decode()
	if err != nil {
		return nil, err
	}
	return parent.Diff(ctx, dir)
}

type dirExportArgs struct {
	Path string
}

func (s *directorySchema) export(ctx *core.Context, parent *core.Directory, args dirExportArgs) (bool, error) {
	err := parent.Export(ctx, s.bk, s.host, s.svcs, args.Path)
	if err != nil {
		return false, err
	}

	return true, nil
}

type dirDockerBuildArgs struct {
	Platform   *specs.Platform
	Dockerfile string
	BuildArgs  []core.BuildArg
	Target     string
	Secrets    []core.SecretID
}

func (s *directorySchema) dockerBuild(ctx *core.Context, parent *core.Directory, args dirDockerBuildArgs) (*core.Container, error) {
	platform := s.platform
	if args.Platform != nil {
		platform = *args.Platform
	}
	ctr, err := core.NewContainer("", parent.Pipeline, platform)
	if err != nil {
		return ctr, err
	}
	return ctr.Build(
		ctx,
		parent,
		args.Dockerfile,
		args.BuildArgs,
		args.Target,
		args.Secrets,
		s.bk,
		s.svcs,
		s.buildCache,
	)
}
