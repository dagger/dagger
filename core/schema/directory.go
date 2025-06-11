package schema

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/moby/patternmatcher/ignorefile"
)

type directorySchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &directorySchema{}

func (s *directorySchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("directory", s.directory).
			Doc(`Creates an empty directory.`),
	}.Install(s.srv)

	dagql.Fields[*core.Directory]{
		Syncer[*core.Directory]().
			Doc(`Force evaluation in the engine.`),
		dagql.Func("pipeline", s.pipeline).
			View(BeforeVersion("v0.13.0")).
			Deprecated("Explicit pipeline creation is now a no-op").
			Doc(`Creates a named sub-pipeline.`).
			Args(
				dagql.Arg("name").Doc("Name of the sub-pipeline."),
				dagql.Arg("description").Doc("Description of the sub-pipeline."),
				dagql.Arg("labels").Doc("Labels to apply to the sub-pipeline."),
			),
		dagql.Func("name", s.name).
			View(AllVersion). // name returns different results in different versions
			Doc(`Returns the name of the directory.`),
		dagql.Func("entries", s.entries).
			View(AllVersion). // entries returns different results in different versions
			Doc(`Returns a list of files and directories at the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location of the directory to look at (e.g., "/src").`),
			),
		dagql.Func("glob", s.glob).
			View(AllVersion). // glob returns different results in different versions
			Doc(`Returns a list of files and directories that matche the given pattern.`).
			Args(
				dagql.Arg("pattern").Doc(`Pattern to match (e.g., "*.md").`),
			),
		dagql.Func("digest", s.digest).
			Doc(
				`Return the directory's digest.
				The format of the digest is not guaranteed to be stable between releases of Dagger.
				It is guaranteed to be stable between invocations of the same Dagger engine.`,
			),
		dagql.Func("file", s.file).
			Doc(`Retrieve a file at the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location of the file to retrieve (e.g., "README.md").`),
			),
		dagql.Func("withFile", s.withFile).
			Doc(`Retrieves this directory plus the contents of the given file copied to the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location of the copied file (e.g., "/file.txt").`),
				dagql.Arg("source").Doc(`Identifier of the file to copy.`),
				dagql.Arg("permissions").Doc(`Permission given to the copied file (e.g., 0600).`),
			),
		dagql.Func("withFiles", s.withFiles).
			Doc(`Retrieves this directory plus the contents of the given files copied to the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location where copied files should be placed (e.g., "/src").`),
				dagql.Arg("sources").Doc(`Identifiers of the files to copy.`),
				dagql.Arg("permissions").Doc(`Permission given to the copied files (e.g., 0600).`),
			),
		dagql.Func("withNewFile", s.withNewFile).
			Doc(`Return a snapshot with a new file added`).
			Args(
				dagql.Arg("path").Doc(`Path of the new file. Example: "foo/bar.txt"`),
				dagql.Arg("contents").Doc(`Contents of the new file. Example: "Hello world!"`),
				dagql.Arg("permissions").Doc(`Permissions of the new file. Example: 0600`),
			),
		dagql.Func("withoutFile", s.withoutFile).
			Doc(`Return a snapshot with a file removed`).
			Args(
				dagql.Arg("path").Doc(`Path of the file to remove (e.g., "/file.txt").`),
			),
		dagql.Func("withoutFiles", s.withoutFiles).
			Doc(`Return a snapshot with files removed`).
			Args(
				dagql.Arg("paths").Doc(`Paths of the files to remove (e.g., ["/file.txt"]).`),
			),
		dagql.Func("directory", s.subdirectory).
			Doc(`Retrieves a directory at the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location of the directory to retrieve. Example: "/src"`),
			),
		dagql.Func("withDirectory", s.withDirectory).
			Doc(`Return a snapshot with a directory added`).
			Args(
				dagql.Arg("path").Doc(`Location of the written directory (e.g., "/src/").`),
				dagql.Arg("directory").Doc(`Identifier of the directory to copy.`),
				dagql.Arg("exclude").Doc(`Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`),
				dagql.Arg("include").Doc(`Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),
			),
		dagql.Func("filter", s.filter).
			Doc(`Return a snapshot with some paths included or excluded`).
			Args(
				dagql.Arg("exclude").Doc(`If set, paths matching one of these glob patterns is excluded from the new snapshot. Example: ["node_modules/", ".git*", ".env"]`),
				dagql.Arg("include").Doc(`If set, only paths matching one of these glob patterns is included in the new snapshot. Example: (e.g., ["app/", "package.*"]).`),
			),
		dagql.Func("withNewDirectory", s.withNewDirectory).
			Doc(`Retrieves this directory plus a new directory created at the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location of the directory created (e.g., "/logs").`),
				dagql.Arg("permissions").Doc(`Permission granted to the created directory (e.g., 0777).`),
			),
		dagql.Func("withoutDirectory", s.withoutDirectory).
			Doc(`Return a snapshot with a subdirectory removed`).
			Args(
				dagql.Arg("path").Doc(`Path of the subdirectory to remove. Example: ".github/workflows"`),
			),
		dagql.Func("diff", s.diff).
			Doc(`Return the difference between this directory and an another directory. The difference is encoded as a directory.`).
			Args(
				dagql.Arg("other").Doc(`The directory to compare against`),
			),
		dagql.Func("export", s.export).
			View(AllVersion).
			DoNotCache("Writes to the local host.").
			Doc(`Writes the contents of the directory to a path on the host.`).
			Args(
				dagql.Arg("path").Doc(`Location of the copied directory (e.g., "logs/").`),
				dagql.Arg("wipe").Doc(`If true, then the host directory will be wiped clean before exporting so that it exactly matches the directory being exported; this means it will delete any files on the host that aren't in the exported dir. If false (the default), the contents of the directory will be merged with any existing contents of the host directory, leaving any existing files on the host that aren't in the exported directory alone.`),
			),
		dagql.Func("export", s.exportLegacy).
			View(BeforeVersion("v0.12.0")).
			Extend(),
		dagql.NodeFunc("dockerBuild", s.dockerBuild).
			Doc(`Use Dockerfile compatibility to build a container from this directory. Only use this function for Dockerfile compatibility. Otherwise use the native Container type directly, it is feature-complete and supports all Dockerfile features.`).
			Args(
				dagql.Arg("dockerfile").Doc(`Path to the Dockerfile to use (e.g., "frontend.Dockerfile").`),
				dagql.Arg("platform").Doc(`The platform to build.`),
				dagql.Arg("buildArgs").Doc(`Build arguments to use in the build.`),
				dagql.Arg("target").Doc(`Target build stage to build.`),
				dagql.Arg("secrets").Doc(`Secrets to pass to the build.`,
					`They will be mounted at /run/secrets/[secret-name].`),
				dagql.Arg("noInit").Doc(
					`If set, skip the automatic init process injected into containers created by RUN statements.`,
					`This should only be used if the user requires that their exec processes be the
				pid 1 process in the container. Otherwise it may result in unexpected behavior.`,
				),
			),
		dagql.Func("withTimestamps", s.withTimestamps).
			Doc(`Retrieves this directory with all file/dir timestamps set to the given time.`).
			Args(
				dagql.Arg("timestamp").Doc(`Timestamp to set dir/files in.`,
					`Formatted in seconds following Unix epoch (e.g., 1672531199).`),
			),
		dagql.Func("asGit", s.asGit).
			Doc(`Converts this directory to a local git repository`),
		dagql.NodeFunc("terminal", s.terminal).
			View(AfterVersion("v0.12.0")).
			DoNotCache("Only creates a temporary container for the user to interact with and then returns original parent.").
			Doc(`Opens an interactive terminal in new container with this directory mounted inside.`).
			Args(
				dagql.Arg("container").Doc(`If set, override the default container used for the terminal.`),
				dagql.Arg("cmd").Doc(`If set, override the container's default terminal command and invoke these command arguments instead.`),
				dagql.Arg("experimentalPrivilegedNesting").Doc(
					`Provides Dagger access to the executed command.`),
				dagql.Arg("insecureRootCapabilities").Doc(
					`Execute the command with all root capabilities. This is similar to
			running a command with "sudo" or executing "docker run" with the
			"--privileged" flag. Containerization does not provide any security
			guarantees when using this option. It should only be used when
			absolutely necessary and only with trusted commands.`),
			),
		dagql.NodeFunc("withSymlink", DagOpDirectoryWrapper(s.srv, s.withSymlink, s.withSymlinkPath)).
			Doc(`Return a snapshot with a symlink`).
			Args(
				dagql.Arg("target").Doc(`Location of the file or directory to link to (e.g., "/existing/file").`),
				dagql.Arg("linkName").Doc(`Location where the symbolic link will be created (e.g., "/new-file-link").`),
			),
	}.Install(s.srv)
}

type directoryPipelineArgs struct {
	Name        string
	Description string                             `default:""`
	Labels      []dagql.InputObject[PipelineLabel] `default:"[]"`
}

func (s *directorySchema) pipeline(ctx context.Context, parent *core.Directory, args directoryPipelineArgs) (*core.Directory, error) {
	// deprecated and a no-op
	return parent, nil
}

func (s *directorySchema) directory(ctx context.Context, parent *core.Query, _ struct{}) (*core.Directory, error) {
	platform := parent.Platform()
	return core.NewScratchDirectory(ctx, platform)
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

type FilterArgs struct {
	core.CopyFilter
}

func (s *directorySchema) filter(ctx context.Context, parent *core.Directory, args FilterArgs) (*core.Directory, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	dir, err := s.directory(ctx, query, struct{}{})
	if err != nil {
		return nil, err
	}

	return dir.WithDirectory(ctx, "/", parent, args.CopyFilter, nil)
}

type dirWithTimestampsArgs struct {
	Timestamp int
}

func (s *directorySchema) withTimestamps(ctx context.Context, parent *core.Directory, args dirWithTimestampsArgs) (*core.Directory, error) {
	return parent.WithTimestamps(ctx, args.Timestamp)
}

func (s *directorySchema) name(ctx context.Context, parent *core.Directory, args struct{}) (dagql.String, error) {
	name := path.Base(parent.Dir)
	useSlash, err := core.SupportsDirSlash(ctx)
	if err != nil {
		return "", err
	}
	if useSlash {
		name = strings.TrimSuffix(name, "/") + "/"
	}
	return dagql.NewString(name), nil
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
	return parent.Glob(ctx, args.Pattern)
}

func (s *directorySchema) digest(ctx context.Context, parent *core.Directory, args struct{}) (dagql.String, error) {
	digest, err := parent.Digest(ctx)
	if err != nil {
		return "", err
	}

	return dagql.NewString(digest), nil
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

type withoutFilesArgs struct {
	Paths []string
}

func (s *directorySchema) withoutFiles(ctx context.Context, parent *core.Directory, args withoutFilesArgs) (*core.Directory, error) {
	return parent.Without(ctx, args.Paths...)
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

func (s *directorySchema) export(ctx context.Context, parent *core.Directory, args dirExportArgs) (dagql.String, error) {
	err := parent.Export(ctx, args.Path, !args.Wipe)
	if err != nil {
		return "", err
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get buildkit client: %w", err)
	}
	stat, err := bk.StatCallerHostPath(ctx, args.Path, true)
	if err != nil {
		return "", err
	}
	return dagql.String(stat.Path), err
}

func (s *directorySchema) exportLegacy(ctx context.Context, parent *core.Directory, args dirExportArgs) (dagql.Boolean, error) {
	_, err := s.export(ctx, parent, args)
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
	NoInit     bool                               `default:"false"`
}

func getDockerIgnoreFileContent(ctx context.Context, parent dagql.Instance[*core.Directory], filename string) ([]byte, error) {
	file, err := parent.Self.File(ctx, filename)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}

		return nil, err
	}

	content, err := file.Contents(ctx)
	if err != nil {
		return nil, err
	}

	return content, nil
}

func applyDockerIgnore(ctx context.Context, srv *dagql.Server, parent dagql.Instance[*core.Directory], dockerfile string) (dagql.Instance[*core.Directory], error) {
	var buildctxDir dagql.Instance[*core.Directory]

	// use dockerfile specific .dockerfile if that exists
	// https://docs.docker.com/build/concepts/context/#filename-and-location
	specificDockerIgnoreFile := dockerfile + ".dockerignore"
	dockerIgnoreContents, err := getDockerIgnoreFileContent(ctx, parent, specificDockerIgnoreFile)
	if err != nil {
		return buildctxDir, err
	}

	// fallback on default .dockerignore file
	if len(dockerIgnoreContents) == 0 {
		dockerIgnoreContents, err = getDockerIgnoreFileContent(ctx, parent, ".dockerignore")
		if err != nil {
			return buildctxDir, err
		}
	}

	excludes, err := ignorefile.ReadAll(bytes.NewBuffer(dockerIgnoreContents))
	if err != nil {
		return buildctxDir, err
	}

	// if no excludes, return the parent directory itself
	if len(excludes) == 0 {
		return parent, nil
	}

	// apply the dockerignore exclusions
	err = srv.Select(ctx, parent, &buildctxDir,
		dagql.Selector{
			Field: "filter",
			Args: []dagql.NamedInput{
				{Name: "exclude", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(excludes...))},
			},
		},
	)
	if err != nil {
		return buildctxDir, err
	}

	return buildctxDir, nil
}

func (s *directorySchema) dockerBuild(ctx context.Context, parent dagql.Instance[*core.Directory], args dirDockerBuildArgs) (*core.Container, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	platform := query.Platform()
	if args.Platform.Valid {
		platform = args.Platform.Value
	}

	buildctxDir, err := applyDockerIgnore(ctx, s.srv, parent, args.Dockerfile)
	if err != nil {
		return nil, err
	}

	ctr, err := core.NewContainer(platform)
	if err != nil {
		return nil, err
	}

	secrets, err := dagql.LoadIDInstances(ctx, s.srv, args.Secrets)
	if err != nil {
		return nil, err
	}
	secretStore, err := query.Secrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret store: %w", err)
	}
	return ctr.Build(
		ctx,
		parent.Self,
		buildctxDir.Self,
		args.Dockerfile,
		collectInputsSlice(args.BuildArgs),
		args.Target,
		secrets,
		secretStore,
		args.NoInit,
	)
}

type directoryTerminalArgs struct {
	core.TerminalArgs
	Container dagql.Optional[core.ContainerID]
}

func (s *directorySchema) terminal(
	ctx context.Context,
	dir dagql.Instance[*core.Directory],
	args directoryTerminalArgs,
) (dagql.Instance[*core.Directory], error) {
	if len(args.Cmd) == 0 {
		args.Cmd = []string{"sh"}
	}

	var ctr *core.Container

	if args.Container.Valid {
		inst, err := args.Container.Value.Load(ctx, s.srv)
		if err != nil {
			return dir, err
		}
		ctr = inst.Self
	}

	err := dir.Self.Terminal(ctx, dir.ID(), ctr, &args.TerminalArgs)
	if err != nil {
		return dir, err
	}

	return dir, nil
}

func (s *directorySchema) asGit(
	ctx context.Context,
	dir *core.Directory,
	_ struct{},
) (*core.GitRepository, error) {
	return &core.GitRepository{
		Backend: &core.LocalGitRepository{
			Directory: dir,
		},
	}, nil
}

type directoryWithSymlinkArgs struct {
	Target   string
	LinkName string
}

func (s *directorySchema) withSymlink(ctx context.Context, parent dagql.Instance[*core.Directory], args directoryWithSymlinkArgs) (inst dagql.Instance[*core.Directory], _ error) {
	dir, err := parent.Self.WithSymlink(ctx, s.srv, args.Target, args.LinkName)
	if err != nil {
		return inst, err
	}
	return dagql.NewInstanceForCurrentID(ctx, s.srv, parent, dir)
}

func (s *directorySchema) withSymlinkPath(ctx context.Context, val dagql.Instance[*core.Directory], _ directoryWithSymlinkArgs) (string, error) {
	return val.Self.Dir, nil
}
