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
			ArgDoc("name", "Name of the sub-pipeline.").
			ArgDoc("description", "Description of the sub-pipeline.").
			ArgDoc("labels", "Labels to apply to the sub-pipeline."),
		dagql.Func("name", s.name).
			Doc(`Returns the name of the directory.`),
		dagql.Func("entries", s.entries).
			Doc(`Returns a list of files and directories at the given path.`).
			ArgDoc("path", `Location of the directory to look at (e.g., "/src").`),
		dagql.Func("glob", s.glob).
			Doc(`Returns a list of files and directories that matche the given pattern.`).
			ArgDoc("pattern", `Pattern to match (e.g., "*.md").`),
		dagql.Func("digest", s.digest).
			Doc(
				`Return the directory's digest.
				The format of the digest is not guaranteed to be stable between releases of Dagger.
				It is guaranteed to be stable between invocations of the same Dagger engine.`,
			),
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
		dagql.Func("withoutFiles", s.withoutFiles).
			Doc(`Retrieves this directory with the files at the given paths removed.`).
			ArgDoc("paths", `Location of the file to remove (e.g., ["/file.txt"]).`),
		dagql.Func("directory", s.subdirectory).
			Doc(`Retrieves a directory at the given path.`).
			ArgDoc("path", `Location of the directory to retrieve (e.g., "/src").`),
		dagql.Func("withDirectory", s.withDirectory).
			Doc(`Retrieves this directory plus a directory written at the given path.`).
			ArgDoc("path", `Location of the written directory (e.g., "/src/").`).
			ArgDoc("directory", `Identifier of the directory to copy.`).
			ArgDoc("exclude", `Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`).
			ArgDoc("include", `Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),
		dagql.Func("filter", s.filter).
			Doc(`Retrieves this directory as per exclude/include filters.`).
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
			View(AllVersion).
			DoNotCache("Writes to the local host.").
			Doc(`Writes the contents of the directory to a path on the host.`).
			ArgDoc("path", `Location of the copied directory (e.g., "logs/").`).
			ArgDoc("wipe", `If true, then the host directory will be wiped clean before exporting so that it exactly matches the directory being exported; this means it will delete any files on the host that aren't in the exported dir. If false (the default), the contents of the directory will be merged with any existing contents of the host directory, leaving any existing files on the host that aren't in the exported directory alone.`),
		dagql.Func("export", s.exportLegacy).
			View(BeforeVersion("v0.12.0")).
			Extend(),
		dagql.NodeFunc("dockerBuild", s.dockerBuildLegacy).
			View(BeforeVersion("v0.18.2")).
			Doc(`Builds a new Docker container from this directory.`).
			ArgDoc("dockerfile", `Path to the Dockerfile to use (e.g., "frontend.Dockerfile").`).
			ArgDoc("platform", `The platform to build.`).
			ArgDoc("buildArgs", `Build arguments to use in the build.`).
			ArgDoc("target", `Target build stage to build.`).
			ArgDoc("secrets", `Secrets to pass to the build.`,
				`They will be mounted at /run/secrets/[secret-name].`).
			ArgDoc("noInit",
				`If set, skip the automatic init process injected into containers created by RUN statements.`,
				`This should only be used if the user requires that their exec processes be the
				pid 1 process in the container. Otherwise it may result in unexpected behavior.`,
			),
		dagql.NodeFunc("dockerBuild", s.dockerBuild).
			View(AfterVersion("v0.18.3")).
			Doc(`Builds a new Docker container from this directory.`).
			ArgDoc("dockerfile", `Path to the Dockerfile to use (e.g., "frontend.Dockerfile").`).
			ArgDoc("platform", `The platform to build.`).
			ArgDoc("buildArgs", `Build arguments to use in the build.`).
			ArgDoc("target", `Target build stage to build.`).
			ArgDoc("secretArgs", `Secrets to pass to the build.`,
				`They will be mounted at /run/secrets/[secret-name].`).
			ArgDoc("noInit",
				`If set, skip the automatic init process injected into containers created by RUN statements.`,
				`This should only be used if the user requires that their exec processes be the
				pid 1 process in the container. Otherwise it may result in unexpected behavior.`,
			),

		dagql.Func("withTimestamps", s.withTimestamps).
			Doc(`Retrieves this directory with all file/dir timestamps set to the given time.`).
			ArgDoc("timestamp", `Timestamp to set dir/files in.`,
				`Formatted in seconds following Unix epoch (e.g., 1672531199).`),
		dagql.Func("asGit", s.asGit).
			Doc(`Converts this directory into a git repository`),
		dagql.NodeFunc("terminal", s.terminal).
			View(AfterVersion("v0.12.0")).
			DoNotCache("Only creates a temporary container for the user to interact with and then returns original parent.").
			Doc(`Opens an interactive terminal in new container with this directory mounted inside.`).
			ArgDoc("container", `If set, override the default container used for the terminal.`).
			ArgDoc("cmd", `If set, override the container's default terminal command and invoke these command arguments instead.`).
			ArgDoc("experimentalPrivilegedNesting",
				`Provides Dagger access to the executed command.`,
				`Do not use this option unless you trust the command being executed;
			the command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST
			FILESYSTEM.`).
			ArgDoc("insecureRootCapabilities",
				`Execute the command with all root capabilities. This is similar to
			running a command with "sudo" or executing "docker run" with the
			"--privileged" flag. Containerization does not provide any security
			guarantees when using this option. It should only be used when
			absolutely necessary and only with trusted commands.`),
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

func (s *directorySchema) directory(ctx context.Context, parent *core.Query, _ struct{}) (*core.Directory, error) {
	platform := parent.Platform()
	return core.NewScratchDirectory(ctx, parent, platform)
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
	dir, err := s.directory(ctx, parent.Query, struct{}{})
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
	useSlash, err := core.SupportsDirSlash(ctx, parent.Query)
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

func (s *directorySchema) exportLegacy(ctx context.Context, parent *core.Directory, args dirExportArgs) (dagql.Boolean, error) {
	_, err := s.export(ctx, parent, args)
	if err != nil {
		return false, err
	}
	return true, nil
}

type dirDockerBuildArgs struct {
	Platform   dagql.Optional[core.Platform]
	Dockerfile string                              `default:"Dockerfile"`
	Target     string                              `default:""`
	BuildArgs  []dagql.InputObject[core.BuildArg]  `default:"[]"`
	SecretArgs []dagql.InputObject[core.SecretArg] `default:"[]"`
	NoInit     bool                                `default:"false"`
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
	platform := parent.Self.Query.Platform()
	if args.Platform.Valid {
		platform = args.Platform.Value
	}

	buildctxDir, err := applyDockerIgnore(ctx, s.srv, parent, args.Dockerfile)
	if err != nil {
		return nil, err
	}

	ctr, err := core.NewContainer(parent.Self.Query, platform)
	if err != nil {
		return nil, err
	}

	secretStore, err := parent.Self.Query.Secrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret store: %w", err)
	}

	vals := make([]core.SecretArgInternal, len(args.SecretArgs))
	for i, arg := range args.SecretArgs {
		secret, ok := secretStore.GetSecret(arg.Value.Value.ID().Digest())
		if !ok {
			return nil, fmt.Errorf("secret %q not found", arg.Value.Value.ID().Digest())
		}

		// if secret name is not explicitly provided, fallback to fetching
		// from the secret store
		secretName := arg.Value.Name
		if arg.Value.Name == "" {
			secretNameFromStore, ok := secretStore.GetSecretName(arg.Value.Value.ID().Digest())
			if !ok {
				return nil, fmt.Errorf("secret %q not found", arg.Value.Value.ID().Digest())
			}
			secretName = secretNameFromStore
		}

		vals[i] = core.SecretArgInternal{
			Name:     secretName,
			Secret:   secret,
			SecretID: args.SecretArgs[i].Value.Value,
		}
	}

	return ctr.Build(
		ctx,
		parent.Self,
		buildctxDir.Self,
		args.Dockerfile,
		collectInputsSlice(args.BuildArgs),
		args.Target,
		vals,
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

type dirDockerBuildArgsLegacy struct {
	Platform   dagql.Optional[core.Platform]
	Dockerfile string                             `default:"Dockerfile"`
	Target     string                             `default:""`
	BuildArgs  []dagql.InputObject[core.BuildArg] `default:"[]"`
	Secrets    []core.SecretID                    `default:"[]"`
	NoInit     bool                               `default:"false"`
}

func (s *directorySchema) dockerBuildLegacy(ctx context.Context, parent dagql.Instance[*core.Directory], args dirDockerBuildArgsLegacy) (*core.Container, error) {
	inps := []dagql.InputObject[core.SecretArg]{}
	for _, secret := range args.Secrets {
		inps = append(inps, dagql.InputObject[core.SecretArg]{
			Value: core.SecretArg{
				Value: secret,
			},
		})
	}

	return s.dockerBuild(ctx, parent, dirDockerBuildArgs{
		Platform:   args.Platform,
		Dockerfile: args.Dockerfile,
		Target:     args.Target,
		BuildArgs:  args.BuildArgs,
		SecretArgs: inps,
		NoInit:     args.NoInit,
	})
}

func (s *directorySchema) asGit(
	ctx context.Context,
	dir *core.Directory,
	_ struct{},
) (*core.GitRepository, error) {
	return &core.GitRepository{
		Backend: &core.LocalGitRepository{
			Query:     dir.Query,
			Directory: dir,
		},
	}, nil
}
