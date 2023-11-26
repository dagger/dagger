package schema

import (
	"context"
	"io/fs"

	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"
)

type directorySchema struct {
	*MergedSchemas

	host *core.Host
	svcs *core.Services
}

var _ ExecutableSchema = &directorySchema{}

func (s *directorySchema) Name() string {
	return "directory"
}

func (s *directorySchema) SourceModuleName() string {
	return coreModuleName
}

func (s *directorySchema) Schema() string {
	return Directory
}

func (s *directorySchema) Resolvers() Resolvers {
	rs := Resolvers{
		"Query": ObjectResolver{
			"directory": ToCachedResolver(s.queryCache, s.directory),
		},
	}

	ResolveIDable[*core.Directory](s.queryCache, rs, "Directory", ObjectResolver{
		"sync":             ToCachedResolver(s.queryCache, s.sync),
		"pipeline":         ToCachedResolver(s.queryCache, s.pipeline),
		"entries":          ToCachedResolver(s.queryCache, s.entries),
		"glob":             ToCachedResolver(s.queryCache, s.glob),
		"file":             ToCachedResolver(s.queryCache, s.file),
		"withFile":         ToCachedResolver(s.queryCache, s.withFile),
		"withNewFile":      ToCachedResolver(s.queryCache, s.withNewFile),
		"withoutFile":      ToCachedResolver(s.queryCache, s.withoutFile),
		"directory":        ToCachedResolver(s.queryCache, s.subdirectory),
		"withDirectory":    ToCachedResolver(s.queryCache, s.withDirectory),
		"withTimestamps":   ToCachedResolver(s.queryCache, s.withTimestamps),
		"withNewDirectory": ToCachedResolver(s.queryCache, s.withNewDirectory),
		"withoutDirectory": ToCachedResolver(s.queryCache, s.withoutDirectory),
		"diff":             ToCachedResolver(s.queryCache, s.diff),
		"export":           ToCachedResolver(s.queryCache, s.export),
		"dockerBuild":      ToCachedResolver(s.queryCache, s.dockerBuild),
	})

	return rs
}

type directoryPipelineArgs struct {
	Name        string
	Description string
	Labels      []pipeline.Label
}

func (s *directorySchema) pipeline(ctx context.Context, parent *core.Directory, args directoryPipelineArgs) (*core.Directory, error) {
	return parent.WithPipeline(ctx, args.Name, args.Description, args.Labels)
}

type directoryArgs struct {
	ID core.DirectoryID
}

func (s *directorySchema) directory(ctx context.Context, parent *core.Query, args directoryArgs) (*core.Directory, error) {
	if args.ID != nil {
		return args.ID.Resolve(s.queryCache)
	}
	platform := s.platform
	return core.NewScratchDirectory(parent.PipelinePath(), platform), nil
}

func (s *directorySchema) sync(ctx context.Context, parent *core.Directory, _ any) (core.DirectoryID, error) {
	_, err := parent.Evaluate(ctx, s.bk, s.svcs)
	if err != nil {
		return nil, err
	}
	return resourceid.FromProto[*core.Directory](parent.ID()), nil
}

type subdirectoryArgs struct {
	Path string
}

func (s *directorySchema) subdirectory(ctx context.Context, parent *core.Directory, args subdirectoryArgs) (*core.Directory, error) {
	return parent.Directory(ctx, s.bk, s.svcs, args.Path)
}

type withNewDirectoryArgs struct {
	Path        string
	Permissions fs.FileMode
}

func (s *directorySchema) withNewDirectory(ctx context.Context, parent *core.Directory, args withNewDirectoryArgs) (*core.Directory, error) {
	return parent.WithNewDirectory(ctx, args.Path, args.Permissions)
}

type withDirectoryArgs struct {
	Path      string
	Directory core.DirectoryID

	core.CopyFilter
}

func (s *directorySchema) withDirectory(ctx context.Context, parent *core.Directory, args withDirectoryArgs) (*core.Directory, error) {
	dir, err := args.Directory.Resolve(s.queryCache)
	if err != nil {
		return nil, err
	}
	return parent.WithDirectory(ctx, args.Path, dir, args.CopyFilter, nil)
}

type dirWithTimestampsArgs struct {
	Timestamp int
}

func (s *directorySchema) withTimestamps(ctx context.Context, parent *core.Directory, args dirWithTimestampsArgs) (*core.Directory, error) {
	return parent.WithTimestamps(ctx, args.Timestamp)
}

type entriesArgs struct {
	Path string
}

func (s *directorySchema) entries(ctx context.Context, parent *core.Directory, args entriesArgs) ([]string, error) {
	return parent.Entries(ctx, s.bk, s.svcs, args.Path)
}

type globArgs struct {
	Pattern string
}

func (s *directorySchema) glob(ctx context.Context, parent *core.Directory, args globArgs) ([]string, error) {
	return parent.Glob(ctx, s.bk, s.svcs, ".", args.Pattern)
}

type dirFileArgs struct {
	Path string
}

func (s *directorySchema) file(ctx context.Context, parent *core.Directory, args dirFileArgs) (_ *core.File, rerr error) {
	return parent.File(ctx, s.bk, s.svcs, args.Path)
}

type withNewFileArgs struct {
	Path        string
	Contents    string
	Permissions fs.FileMode
}

func (s *directorySchema) withNewFile(ctx context.Context, parent *core.Directory, args withNewFileArgs) (*core.Directory, error) {
	return parent.WithNewFile(ctx, args.Path, []byte(args.Contents), args.Permissions, nil)
}

type withFileArgs struct {
	Path        string
	Source      core.FileID
	Permissions fs.FileMode
}

func (s *directorySchema) withFile(ctx context.Context, parent *core.Directory, args withFileArgs) (*core.Directory, error) {
	file, err := args.Source.Resolve(s.queryCache)
	if err != nil {
		return nil, err
	}

	return parent.WithFile(ctx, args.Path, file, args.Permissions, nil)
}

type withoutDirectoryArgs struct {
	Path string
}

func (s *directorySchema) withoutDirectory(ctx context.Context, parent *core.Directory, args withoutDirectoryArgs) (*core.Directory, error) {
	return parent.Without(ctx, args.Path)
}

type withoutFileArgs struct {
	Path string
}

func (s *directorySchema) withoutFile(ctx context.Context, parent *core.Directory, args withoutFileArgs) (*core.Directory, error) {
	return parent.Without(ctx, args.Path)
}

type diffArgs struct {
	Other core.DirectoryID
}

func (s *directorySchema) diff(ctx context.Context, parent *core.Directory, args diffArgs) (*core.Directory, error) {
	dir, err := args.Other.Resolve(s.queryCache)
	if err != nil {
		return nil, err
	}
	return parent.Diff(ctx, dir)
}

type dirExportArgs struct {
	Path string
}

func (s *directorySchema) export(ctx context.Context, parent *core.Directory, args dirExportArgs) (bool, error) {
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

func (s *directorySchema) dockerBuild(ctx context.Context, parent *core.Directory, args dirDockerBuildArgs) (*core.Container, error) {
	platform := s.platform
	if args.Platform != nil {
		platform = *args.Platform
	}
	ctr, err := core.NewContainer(parent.Pipeline, platform)
	if err != nil {
		return ctr, err
	}
	secrets := make([]*core.Secret, len(args.Secrets))
	for i, id := range args.Secrets {
		secrets[i], err = id.Resolve(s.queryCache)
		if err != nil {
			return nil, err
		}
	}
	return ctr.Build(
		ctx,
		parent,
		args.Dockerfile,
		args.BuildArgs,
		args.Target,
		secrets,
		s.bk,
		s.svcs,
	)
}
