package schema

import (
	"context"
	"io/fs"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type directorySchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &directorySchema{}

func (s *directorySchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("directory", s.directory).
			Doc(`Creates an empty directory.`).
			ArgDeprecated("id", "Use `loadDirectoryFromID` instead."),
	}.Install(s.srv)

	dagql.Fields[*core.Directory]{
		Syncer[*core.Directory]().
			Doc(`Force evaluation in the engine.`),
		dagql.Func("pipeline", s.pipeline).
			Doc(`Creates a named sub-pipeline.`).
			ArgDoc("name", "Name of the sub-pipeline.").
			ArgDoc("description", "Description of the sub-pipeline.").
			ArgDoc("labels", "Labels to apply to the sub-pipeline."),
		dagql.Func("entries", s.entries).
			Doc(`Returns a list of files and directories at the given path.`).
			ArgDoc("path", `Location of the directory to look at (e.g., "/src").`),
		dagql.Func("glob", s.glob).
			Doc(`Returns a list of files and directories that matche the given pattern.`).
			ArgDoc("pattern", `Pattern to match (e.g., "*.md").`),
		dagql.Func("file", s.file).
			Doc(`Retrieves a file at the given path.`).
			ArgDoc("path", `Location of the file to retrieve (e.g., "README.md").`),
		dagql.Func("withFile", s.withFile).
			Doc(`Retrieves this directory plus the contents of the given file copied to the given path.`).
			ArgDoc("path", `Location of the copied file (e.g., "/file.txt").`).
			ArgDoc("source", `Identifier of the file to copy.`).
			ArgDoc("permissions", `Permission given to the copied file (e.g., 0600).`),
		dagql.Func("withFiles", s.withFiles).
			Doc(`Retrieves this directory plus the contents of the given files copied to the given path.`).
			ArgDoc("path", `Location where copied files should be placed (e.g., "/src").`).
			ArgDoc("sources", `Identifiers of the files to copy.`).
			ArgDoc("permissions", `Permission given to the copied files (e.g., 0600).`),
		dagql.Func("withNewFile", s.withNewFile).
			Doc(`Retrieves this directory plus a new file written at the given path.`).
			ArgDoc("path", `Location of the written file (e.g., "/file.txt").`).
			ArgDoc("contents", `Content of the written file (e.g., "Hello world!").`).
			ArgDoc("permissions", `Permission given to the copied file (e.g., 0600).`),
		dagql.Func("withoutFile", s.withoutFile).
			Doc(`Retrieves this directory with the file at the given path removed.`).
			ArgDoc("path", `Location of the file to remove (e.g., "/file.txt").`),
		dagql.Func("directory", s.subdirectory).
			Doc(`Retrieves a directory at the given path.`).
			ArgDoc("path", `Location of the directory to retrieve (e.g., "/src").`),
		dagql.Func("withDirectory", s.withDirectory).
			Doc(`Retrieves this directory plus a directory written at the given path.`).
			ArgDoc("path", `Location of the written directory (e.g., "/src/").`).
			ArgDoc("directory", `Identifier of the directory to copy.`).
			ArgDoc("exclude", `Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`).
			ArgDoc("include", `Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),
		dagql.Func("withNewDirectory", s.withNewDirectory).
			Doc(`Retrieves this directory plus a new directory created at the given path.`).
			ArgDoc("path", `Location of the directory created (e.g., "/logs").`).
			ArgDoc("permissions", `Permission granted to the created directory (e.g., 0777).`),
		dagql.Func("withoutDirectory", s.withoutDirectory).
			Doc(`Retrieves this directory with the directory at the given path removed.`).
			ArgDoc("path", `Location of the directory to remove (e.g., ".github/").`),
		dagql.Func("diff", s.diff).
			Doc(`Gets the difference between this directory and an another directory.`).
			ArgDoc("other", `Identifier of the directory to compare.`),
		dagql.Func("export", s.export).
			Impure("Writes to the local host.").
			Doc(`Writes the contents of the directory to a path on the host.`).
			ArgDoc("path", `Location of the copied directory (e.g., "logs/").`).
			ArgDoc("wipe", `If true, then the host directory will be wiped clean before exporting so that it exactly matches the directory being exported; this means it will delete any files on the host that aren't in the exported dir. If false (the default), the contents of the directory will be merged with any existing contents of the host directory, leaving any existing files on the host that aren't in the exported directory alone.`),
		dagql.Func("dockerBuild", s.dockerBuild).
			Doc(`Builds a new Docker container from this directory.`).
			ArgDoc("dockerfile", `Path to the Dockerfile to use (e.g., "frontend.Dockerfile").`).
			ArgDoc("platform", `The platform to build.`).
			ArgDoc("buildArgs", `Build arguments to use in the build.`).
			ArgDoc("target", `Target build stage to build.`).
			ArgDoc("secrets", `Secrets to pass to the build.`,
				`They will be mounted at /run/secrets/[secret-name].`),
		dagql.Func("withTimestamps", s.withTimestamps).
			Doc(`Retrieves this directory with all file/dir timestamps set to the given time.`).
			ArgDoc("timestamp", `Timestamp to set dir/files in.`,
				`Formatted in seconds following Unix epoch (e.g., 1672531199).`),
	}.Install(s.srv)
}

type directoryPipelineArgs struct {
	Name        string
	Description string                             `default:""`
	Labels      []dagql.InputObject[PipelineLabel] `default:"[]"`
}

func (s *directorySchema) pipeline(ctx context.Context, parent *core.Directory, args directoryPipelineArgs) (*core.Directory, error) {
	return parent.WithPipeline(ctx, args.Name, args.Description)
}

type directoryArgs struct {
	ID dagql.Optional[core.DirectoryID]
}

func (s *directorySchema) directory(ctx context.Context, parent *core.Query, args directoryArgs) (*core.Directory, error) {
	if args.ID.Valid {
		inst, err := args.ID.Value.Load(ctx, s.srv)
		if err != nil {
			return nil, err
		}
		return inst.Self, nil
	}
	platform := parent.Platform
	return core.NewScratchDirectory(parent, platform), nil
}

type subdirectoryArgs struct {
	Path string
}

func (s *directorySchema) subdirectory(ctx context.Context, parent *core.Directory, args subdirectoryArgs) (*core.Directory, error) {
	return parent.Directory(ctx, args.Path)
}

type withNewDirectoryArgs struct {
	Path        string
	Permissions int `default:"0644"`
}

func (s *directorySchema) withNewDirectory(ctx context.Context, parent *core.Directory, args withNewDirectoryArgs) (*core.Directory, error) {
	return parent.WithNewDirectory(ctx, args.Path, fs.FileMode(args.Permissions))
}

type WithDirectoryArgs struct {
	Path      string
	Directory core.DirectoryID

	core.CopyFilter
}

func (s *directorySchema) withDirectory(ctx context.Context, parent *core.Directory, args WithDirectoryArgs) (*core.Directory, error) {
	dir, err := args.Directory.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return parent.WithDirectory(ctx, args.Path, dir.Self, args.CopyFilter, nil)
}

type dirWithTimestampsArgs struct {
	Timestamp int
}

func (s *directorySchema) withTimestamps(ctx context.Context, parent *core.Directory, args dirWithTimestampsArgs) (*core.Directory, error) {
	return parent.WithTimestamps(ctx, args.Timestamp)
}

type entriesArgs struct {
	Path dagql.Optional[dagql.String]
}

func (s *directorySchema) entries(ctx context.Context, parent *core.Directory, args entriesArgs) (dagql.Array[dagql.String], error) {
	ents, err := parent.Entries(ctx, args.Path.Value.String())
	if err != nil {
		return nil, err
	}
	return dagql.NewStringArray(ents...), nil
}

type globArgs struct {
	Pattern string
}

func (s *directorySchema) glob(ctx context.Context, parent *core.Directory, args globArgs) ([]string, error) {
	return parent.Glob(ctx, ".", args.Pattern)
}

type dirFileArgs struct {
	Path string
}

func (s *directorySchema) file(ctx context.Context, parent *core.Directory, args dirFileArgs) (*core.File, error) {
	return parent.File(ctx, args.Path)
}

func (s *directorySchema) withNewFile(ctx context.Context, parent *core.Directory, args struct {
	Path        string
	Contents    string
	Permissions int `default:"0644"`
}) (*core.Directory, error) {
	return parent.WithNewFile(ctx, args.Path, []byte(args.Contents), fs.FileMode(args.Permissions), nil)
}

type WithFileArgs struct {
	Path        string
	Source      core.FileID
	Permissions *int
}

func (s *directorySchema) withFile(ctx context.Context, parent *core.Directory, args WithFileArgs) (*core.Directory, error) {
	file, err := args.Source.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}

	return parent.WithFile(ctx, args.Path, file.Self, args.Permissions, nil)
}

type WithFilesArgs struct {
	Path        string
	Sources     []core.FileID
	Permissions *int
}

func (s *directorySchema) withFiles(ctx context.Context, parent *core.Directory, args WithFilesArgs) (*core.Directory, error) {
	files := []*core.File{}
	for _, id := range args.Sources {
		file, err := id.Load(ctx, s.srv)
		if err != nil {
			return nil, err
		}
		files = append(files, file.Self)
	}

	return parent.WithFiles(ctx, args.Path, files, args.Permissions, nil)
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
	dir, err := args.Other.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return parent.Diff(ctx, dir.Self)
}

type dirExportArgs struct {
	Path string
	Wipe bool `default:"false"`
}

func (s *directorySchema) export(ctx context.Context, parent *core.Directory, args dirExportArgs) (dagql.Boolean, error) {
	err := parent.Export(ctx, args.Path, !args.Wipe)
	if err != nil {
		return false, err
	}

	return true, nil
}

type dirDockerBuildArgs struct {
	Platform   dagql.Optional[core.Platform]
	Dockerfile string                             `default:"Dockerfile"`
	Target     string                             `default:""`
	BuildArgs  []dagql.InputObject[core.BuildArg] `default:"[]"`
	Secrets    []core.SecretID                    `default:"[]"`
}

func (s *directorySchema) dockerBuild(ctx context.Context, parent *core.Directory, args dirDockerBuildArgs) (*core.Container, error) {
	platform := parent.Query.Platform
	if args.Platform.Valid {
		platform = args.Platform.Value
	}
	ctr, err := core.NewContainer(parent.Query, platform)
	if err != nil {
		return nil, err
	}
	secrets, err := dagql.LoadIDs(ctx, s.srv, args.Secrets)
	if err != nil {
		return nil, err
	}
	return ctr.Build(
		ctx,
		parent,
		args.Dockerfile,
		collectInputsSlice(args.BuildArgs),
		args.Target,
		secrets,
	)
}
