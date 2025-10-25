package schema

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/moby/patternmatcher/ignorefile"
	"github.com/opencontainers/go-digest"
)

type directorySchema struct{}

var _ SchemaResolvers = &directorySchema{}

func (s *directorySchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("directory", s.directory).
			Doc(`Creates an empty directory.`),
		dagql.NodeFunc("__immutableRef", DagOpDirectoryWrapper(srv, s.immutableRef)).
			Doc(`Returns a directory backed by a pre-existing immutable ref.`).
			Args(dagql.Arg("ref").Doc("The immutable ref ID.")),
	}.Install(srv)

	core.ExistsTypes.Install(srv)

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
		dagql.NodeFunc("entries", DagOpWrapper(srv, s.entries)).
			View(AllVersion). // entries returns different results in different versions
			Doc(`Returns a list of files and directories at the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location of the directory to look at (e.g., "/src").`),
			),
		dagql.NodeFunc("glob", DagOpWrapper(srv, s.glob)).
			View(AllVersion). // glob returns different results in different versions
			Doc(`Returns a list of files and directories that matche the given pattern.`).
			Args(
				dagql.Arg("pattern").Doc(`Pattern to match (e.g., "*.md").`),
			),
		dagql.NodeFunc("search", DagOpWrapper(srv, s.search)).
			Doc(
				// NOTE: sync with File.search
				`Searches for content matching the given regular expression or literal string.`,
				`Uses Rust regex syntax; escape literal ., [, ], {, }, | with backslashes.`,
			).
			Args((func() []dagql.Argument {
				args := []dagql.Argument{
					dagql.Arg("paths").Doc("Directory or file paths to search"),
					dagql.Arg("globs").Doc("Glob patterns to match (e.g., \"*.md\")"),
				}
				args = append(args, (core.SearchOpts{}).Args()...)
				return args
			})()...),
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
		dagql.NodeFunc("withFile", DagOpDirectoryWrapper(srv, s.withFile, WithPathFn(keepParentDir[WithFileArgs]))).
			Doc(`Retrieves this directory plus the contents of the given file copied to the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location of the copied file (e.g., "/file.txt").`),
				dagql.Arg("source").Doc(`Identifier of the file to copy.`),
				dagql.Arg("permissions").Doc(`Permission given to the copied file (e.g., 0600).`),
				dagql.Arg("owner").Doc(`A user:group to set for the copied directory and its contents.`,
					`The user and group must be an ID (1000:1000), not a name (foo:bar).`,
					`If the group is omitted, it defaults to the same as the user.`),
			),
		dagql.NodeFunc("withFiles", DagOpDirectoryWrapper(srv, s.withFiles, WithPathFn(keepParentDir[WithFilesArgs]))).
			Doc(`Retrieves this directory plus the contents of the given files copied to the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location where copied files should be placed (e.g., "/src").`),
				dagql.Arg("sources").Doc(`Identifiers of the files to copy.`),
				dagql.Arg("permissions").Doc(`Permission given to the copied files (e.g., 0600).`),
			),
		dagql.NodeFunc("withNewFile", DagOpDirectoryWrapper(srv, s.withNewFile, WithPathFn(keepParentDir[WithNewFileArgs]))).
			Doc(`Return a snapshot with a new file added`).
			Args(
				dagql.Arg("path").Doc(`Path of the new file. Example: "foo/bar.txt"`),
				dagql.Arg("contents").Doc(`Contents of the new file. Example: "Hello world!"`),
				dagql.Arg("permissions").Doc(`Permissions of the new file. Example: 0600`),
			),
		dagql.NodeFunc("withoutFile", DagOpDirectoryWrapper(srv, s.withoutFile, WithPathFn(keepParentDir[withoutFileArgs]))).
			Doc(`Return a snapshot with a file removed`).
			Args(
				dagql.Arg("path").Doc(`Path of the file to remove (e.g., "/file.txt").`),
			),
		dagql.NodeFunc("withoutFiles", DagOpDirectoryWrapper(srv, s.withoutFiles, WithPathFn(keepParentDir[withoutFilesArgs]))).
			Doc(`Return a snapshot with files removed`).
			Args(
				dagql.Arg("paths").Doc(`Paths of the files to remove (e.g., ["/file.txt"]).`),
			),
		dagql.Func("exists", s.exists).
			Doc(`check if a file or directory exists`).
			Args(
				dagql.Arg("path").Doc(`Path to check (e.g., "/file.txt").`),
				dagql.Arg("expectedType").Doc(`If specified, also validate the type of file (e.g. "REGULAR_TYPE", "DIRECTORY_TYPE", or "SYMLINK_TYPE").`),
				dagql.Arg("doNotFollowSymlinks").Doc(`If specified, do not follow symlinks.`),
			),
		dagql.NodeFunc("directory", maintainContentHashing(s.subdirectory)).
			Doc(`Retrieves a directory at the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location of the directory to retrieve. Example: "/src"`),
			),
		dagql.NodeFunc("withDirectory", DagOpDirectoryWrapper(srv, s.withDirectory, WithPathFn(keepParentDir[WithDirectoryArgs]))).
			View(AllVersion).
			Doc(`Return a snapshot with a directory added`).
			Args(
				dagql.Arg("path").Doc(`Location of the written directory (e.g., "/src/").`),
				dagql.Arg("directory").Doc(`Identifier of the directory to copy.`).View(BeforeVersion("v0.19.0")),
				dagql.Arg("source").Doc(`Identifier of the directory to copy.`).View(AfterVersion("v0.19.0")),
				dagql.Arg("exclude").Doc(`Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`),
				dagql.Arg("include").Doc(`Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),
				dagql.Arg("owner").Doc(`A user:group to set for the copied directory and its contents.`,
					`The user and group must be an ID (1000:1000), not a name (foo:bar).`,
					`If the group is omitted, it defaults to the same as the user.`),
			),
		dagql.NodeFunc("filter", DagOpDirectoryWrapper(srv, s.filter, WithPathFn(keepParentDir[FilterArgs]))).
			Doc(`Return a snapshot with some paths included or excluded`).
			Args(
				dagql.Arg("exclude").Doc(`If set, paths matching one of these glob patterns is excluded from the new snapshot. Example: ["node_modules/", ".git*", ".env"]`),
				dagql.Arg("include").Doc(`If set, only paths matching one of these glob patterns is included in the new snapshot. Example: (e.g., ["app/", "package.*"]).`),
			),
		dagql.NodeFunc("withNewDirectory", DagOpDirectoryWrapper(srv, s.withNewDirectory, WithPathFn(keepParentDir[withNewDirectoryArgs]))).
			Doc(`Retrieves this directory plus a new directory created at the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location of the directory created (e.g., "/logs").`),
				dagql.Arg("permissions").Doc(`Permission granted to the created directory (e.g., 0777).`),
			),
		dagql.NodeFunc("withoutDirectory", DagOpDirectoryWrapper(srv, s.withoutDirectory, WithPathFn(keepParentDir[withoutDirectoryArgs]))).
			Doc(`Return a snapshot with a subdirectory removed`).
			Args(
				dagql.Arg("path").Doc(`Path of the subdirectory to remove. Example: ".github/workflows"`),
			),
		dagql.Func("diff", s.diff).
			Doc(`Return the difference between this directory and an another directory. The difference is encoded as a directory.`).
			Args(
				dagql.Arg("other").Doc(`The directory to compare against`),
			),
		dagql.Func("findUp", s.findUp).
			Doc(`Search up the directory tree for a file or directory, and return its path. If no match, return null`).
			Args(
				dagql.Arg("name").Doc(`The name of the file or directory to search for`),
				dagql.Arg("start").Doc(`The path to start the search from`),
			),
		dagql.NodeFunc("changes", s.changes).
			Doc(
				`Return the difference between this directory and another directory, typically an older snapshot.`,
				`The difference is encoded as a changeset, which also tracks removed files, and can be applied to other directories.`,
			).
			Args(
				dagql.Arg("from").Doc(`The base directory snapshot to compare against`),
			),
		dagql.NodeFunc("withChanges", DagOpDirectoryWrapper(srv, s.withChanges, WithPathFn(keepParentDir[withChangesArgs]))).
			Doc(`Return a directory with changes from another directory applied to it.`).
			Args(
				dagql.Arg("changes").Doc(`Changes to apply to the directory`),
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
		dagql.NodeFunc("withTimestamps", DagOpDirectoryWrapper(srv, s.withTimestamps, WithPathFn(keepParentDir[dirWithTimestampsArgs]))).
			Doc(`Retrieves this directory with all file/dir timestamps set to the given time.`).
			Args(
				dagql.Arg("timestamp").Doc(`Timestamp to set dir/files in.`,
					`Formatted in seconds following Unix epoch (e.g., 1672531199).`),
			),
		dagql.NodeFunc("withPatch",
			DagOpDirectoryWrapper(srv, s.withPatch,
				WithPathFn(keepParentDir[withPatchArgs]))).
			Experimental("This API is highly experimental and may be removed or replaced entirely.").
			Doc(`Retrieves this directory with the given Git-compatible patch applied.`).
			Args(
				dagql.Arg("patch").Doc(`Patch to apply (e.g., "diff --git a/file.txt b/file.txt\nindex 1234567..abcdef8 100644\n--- a/file.txt\n+++ b/file.txt\n@@ -1,1 +1,1 @@\n-Hello\n+World\n").`),
			),
		dagql.NodeFunc("withPatchFile",
			DagOpDirectoryWrapper(srv, s.withPatchFile,
				WithPathFn(keepParentDir[withPatchFileArgs]))).
			Experimental("This API is highly experimental and may be removed or replaced entirely.").
			Doc(`Retrieves this directory with the given Git-compatible patch file applied.`).
			Args(
				dagql.Arg("patch").Doc(`File containing the patch to apply`),
			),
		dagql.NodeFunc("asGit", s.asGit).
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
		dagql.NodeFunc("withSymlink", DagOpDirectoryWrapper(srv, s.withSymlink, WithPathFn(keepParentDir[directoryWithSymlinkArgs]))).
			Doc(`Return a snapshot with a symlink`).
			Args(
				dagql.Arg("target").Doc(`Location of the file or directory to link to (e.g., "/existing/file").`),
				dagql.Arg("linkName").Doc(`Location where the symbolic link will be created (e.g., "/new-file-link").`),
			),
		dagql.NodeFunc("chown", DagOpDirectoryWrapper(srv, s.chown, WithPathFn(keepParentDir[directoryChownArgs]))).
			Doc(`Change the owner of the directory contents recursively.`).
			Args(
				dagql.Arg("path").Doc(`Path of the directory to change ownership of (e.g., "/").`),
				dagql.Arg("owner").Doc(`A user:group to set for the mounted directory and its contents.`,
					`The user and group must be an ID (1000:1000), not a name (foo:bar).`,
					`If the group is omitted, it defaults to the same as the user.`),
			),
		dagql.NodeFunc("withError", s.withError).
			Doc(`Raise an error.`).
			Args(
				dagql.Arg("err").Doc(`Message of the error to raise. If empty, the error will be ignored.`),
			),
	}.Install(srv)

	dagql.Fields[*core.SearchResult]{}.Install(srv)
	dagql.Fields[*core.SearchSubmatch]{}.Install(srv)

	dagql.Fields[*core.Changeset]{
		Syncer[*core.Changeset]().
			Doc(`Force evaluation in the engine.`),
		dagql.NodeFunc("layer", DagOpDirectoryWrapper(srv, s.changesetLayer)).
			Doc(`Return a snapshot containing only the created and modified files`),
		dagql.NodeFunc("asPatch", DagOpFileWrapper(srv, s.changesetAsPatch,
			WithStaticPath[*core.Changeset, changesetAsPatchArgs](core.ChangesetPatchFilename))).
			Doc(`Return a Git-compatible patch of the changes`),
		dagql.Func("export", s.changesetExport).
			DoNotCache("Writes to the local host.").
			Doc(`Applies the diff represented by this changeset to a path on the host.`).
			Args(
				dagql.Arg("path").Doc(`Location of the copied directory (e.g., "logs/").`),
			),
		dagql.NodeFunc("isEmpty", s.changesetEmpty).
			Doc(`Returns true if the changeset is empty (i.e. there are no changes).`),
	}.Install(srv)
}

type directoryPipelineArgs struct {
	Name        string
	Description string                             `default:""`
	Labels      []dagql.InputObject[PipelineLabel] `default:"[]"`
}

func (s *directorySchema) immutableRef(ctx context.Context, parent dagql.ObjectResult[*core.Query], args struct {
	Ref string
	DagOpInternalArgs
}) (res dagql.ObjectResult[*core.Directory], _ error) {
	query := parent.Self()
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, err
	}
	immutable, err := query.BuildkitCache().Get(ctx, args.Ref, nil)
	if err != nil {
		return res, fmt.Errorf("failed to get immutable ref %q: %w", args.Ref, err)
	}
	dir, err := core.NewScratchDirectory(ctx, query.Platform())
	if err != nil {
		return res, fmt.Errorf("failed to create scratch directory: %w", err)
	}
	dir.Result = immutable.Clone() // FIXME(vito): is this Clone redundant/harmful?
	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
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

func (s *directorySchema) subdirectory(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Directory],
	args subdirectoryArgs,
) (res dagql.ObjectResult[*core.Directory], err error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return res, err
	}
	srv, err := query.Server.Server(ctx)
	if err != nil {
		return res, err
	}
	dir, err := parent.Self().Directory(ctx, args.Path)
	if err != nil {
		return res, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
}

type withNewDirectoryArgs struct {
	Path        string
	Permissions int `default:"0644"`

	FSDagOpInternalArgs
}

func (s *directorySchema) withNewDirectory(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args withNewDirectoryArgs) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	dir, err := parent.Self().WithNewDirectory(ctx, args.Path, fs.FileMode(args.Permissions))
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
}

type WithDirectoryArgs struct {
	Path  string
	Owner string `default:""`

	Source    core.DirectoryID
	Directory core.DirectoryID // legacy, use Source instead

	core.CopyFilter
	DagOpInternalArgs
}

var _ core.Inputs = WithDirectoryArgs{}

func (args WithDirectoryArgs) Inputs(ctx context.Context) ([]llb.State, error) {
	deps := []llb.State{}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	if args.Source.ID() == nil {
		return nil, nil
	}

	sourceRes, err := args.Source.Load(ctx, srv)
	if err != nil {
		return nil, fmt.Errorf("load source: %w", err)
	}
	sourceOp, err := llb.NewDefinitionOp(sourceRes.Self().LLB)
	if err != nil {
		return nil, fmt.Errorf("source op: %w", err)
	}
	if sourceOp.Output() != nil {
		deps = append(deps, llb.NewState(sourceOp))
	}

	return deps, nil
}

func (s *directorySchema) withDirectory(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args WithDirectoryArgs) (res dagql.ObjectResult[*core.Directory], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, err
	}
	src := cmp.Or(args.Source, args.Directory)
	with, err := parent.Self().WithDirectory(ctx, args.Path, src.ID(), args.CopyFilter, args.Owner)
	if err != nil {
		return res, fmt.Errorf("failed to add directory %q: %w", args.Path, err)
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, with)
}

type FilterArgs struct {
	core.CopyFilter

	DagOpInternalArgs
}

func (s *directorySchema) filter(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args FilterArgs) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	platform := parent.Self().Platform
	scratchDir := core.Directory{
		Platform: platform,
		Dir:      parent.Self().Dir,
	}

	filtered, err := scratchDir.WithDirectory(ctx, "/", parent.ID(), args.CopyFilter, "")
	if err != nil {
		return inst, fmt.Errorf("failed to filter: %w", err)
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, filtered)
}

type dirWithTimestampsArgs struct {
	Timestamp int

	FSDagOpInternalArgs
}

func (s *directorySchema) withTimestamps(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args dirWithTimestampsArgs) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	dir, err := parent.Self().WithTimestamps(ctx, args.Timestamp)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
}

func (s *directorySchema) name(ctx context.Context, parent *core.Directory, args struct{}) (dagql.String, error) {
	name := path.Base(parent.Dir)
	if core.SupportsDirSlash(ctx) {
		name = strings.TrimSuffix(name, "/") + "/"
	}
	return dagql.NewString(name), nil
}

type entriesArgs struct {
	Path dagql.Optional[dagql.String]

	RawDagOpInternalArgs
}

func (s *directorySchema) entries(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args entriesArgs) (dagql.Array[dagql.String], error) {
	ents, err := parent.Self().Entries(ctx, args.Path.Value.String())
	if err != nil {
		return nil, err
	}
	return dagql.NewStringArray(ents...), nil
}

type globArgs struct {
	Pattern string

	RawDagOpInternalArgs
}

func (s *directorySchema) glob(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args globArgs) (dagql.Array[dagql.String], error) {
	ents, err := parent.Self().Glob(ctx, args.Pattern)
	if err != nil {
		return nil, err
	}
	return dagql.NewStringArray(ents...), nil
}

type searchArgs struct {
	core.SearchOpts
	Paths []string `default:"[]"`
	Globs []string `default:"[]"`
	RawDagOpInternalArgs
}

func (s *directorySchema) search(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args searchArgs) (dagql.Array[*core.SearchResult], error) {
	return parent.Self().Search(ctx, args.SearchOpts, args.Paths, args.Globs)
}

type withPatchArgs struct {
	Patch string

	FSDagOpInternalArgs
}

func (s *directorySchema) withPatch(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args withPatchArgs) (inst dagql.ObjectResult[*core.Directory], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	dir, err := parent.Self().WithPatch(ctx, args.Patch)
	if err != nil {
		return inst, err
	}

	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
}

type withPatchFileArgs struct {
	Patch core.FileID

	FSDagOpInternalArgs
}

func (s *directorySchema) withPatchFile(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args withPatchFileArgs) (inst dagql.ObjectResult[*core.Directory], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	patchFile, err := args.Patch.Load(ctx, srv)
	if err != nil {
		return inst, err
	}
	// FIXME: would be nice to avoid reading into memory, need to adjust WithPatch
	// for that
	patch, err := patchFile.Self().Contents(ctx, nil, nil)
	if err != nil {
		return inst, err
	}
	dir, err := parent.Self().WithPatch(ctx, string(patch))
	if err != nil {
		return inst, err
	}

	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
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

type WithNewFileArgs struct {
	Path        string
	Contents    string
	Permissions int `default:"0644"`

	FSDagOpInternalArgs
}

func (s *directorySchema) withNewFile(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args WithNewFileArgs) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	dir, err := parent.Self().WithNewFileDagOp(ctx, args.Path, []byte(args.Contents), fs.FileMode(args.Permissions), nil)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
}

type WithFileArgs struct {
	Path        string
	Source      core.FileID
	Permissions dagql.Optional[dagql.Int]
	Owner       string `default:""`

	FSDagOpInternalArgs
}

func (s *directorySchema) withFile(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args WithFileArgs) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	file, err := args.Source.Load(ctx, srv)
	if err != nil {
		return inst, err
	}

	var perms *int
	if args.Permissions.Valid {
		p := int(args.Permissions.Value)
		perms = &p
	}
	dir, err := parent.Self().WithFile(ctx, srv, args.Path, file.Self(), perms, args.Owner)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
}

func keepParentDir[A any](_ context.Context, val *core.Directory, _ A) (string, error) {
	return val.Dir, nil
}

type WithFilesArgs struct {
	Path        string
	Sources     []core.FileID
	Permissions dagql.Optional[dagql.Int]

	FSDagOpInternalArgs
}

func (s *directorySchema) withFiles(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args WithFilesArgs) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	files := []*core.File{}
	for _, id := range args.Sources {
		file, err := id.Load(ctx, srv)
		if err != nil {
			return inst, err
		}
		files = append(files, file.Self())
	}

	var perms *int
	if args.Permissions.Valid {
		p := int(args.Permissions.Value)
		perms = &p
	}
	dir, err := parent.Self().WithFiles(ctx, srv, args.Path, files, perms)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
}

type withoutDirectoryArgs struct {
	Path string

	FSDagOpInternalArgs
}

func (s *directorySchema) withoutDirectory(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args withoutDirectoryArgs) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	dir, err := parent.Self().Without(ctx, srv, args.Path)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
}

type withoutFileArgs struct {
	Path string

	FSDagOpInternalArgs
}

func (s *directorySchema) withoutFile(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args withoutFileArgs) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	dir, err := parent.Self().Without(ctx, srv, args.Path)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
}

type withoutFilesArgs struct {
	Paths []string

	FSDagOpInternalArgs
}

func (s *directorySchema) withoutFiles(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args withoutFilesArgs) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	dir, err := parent.Self().Without(ctx, srv, args.Paths...)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
}

type existsArgs struct {
	Path                string
	ExpectedType        dagql.Optional[core.ExistsType]
	DoNotFollowSymlinks bool `default:"false"`
}

func (s *directorySchema) exists(ctx context.Context, parent *core.Directory, args existsArgs) (dagql.Boolean, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return false, err
	}

	exists, err := parent.Exists(ctx, srv, args.Path, args.ExpectedType.Value, args.DoNotFollowSymlinks)
	return dagql.NewBoolean(exists), err
}

type diffArgs struct {
	Other core.DirectoryID
}

func (s *directorySchema) diff(ctx context.Context, parent *core.Directory, args diffArgs) (*core.Directory, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}

	dir, err := args.Other.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	return parent.Diff(ctx, dir.Self())
}

type findUpArgs struct {
	Name  string
	Start string
}

func (s *directorySchema) findUp(ctx context.Context, parent *core.Directory, args findUpArgs) (dagql.Nullable[dagql.String], error) {
	none := dagql.Null[dagql.String]()
	path, err := parent.FindUp(ctx, args.Name, args.Start)
	if err != nil {
		return none, err
	}
	if path == "" {
		return none, nil
	}
	return dagql.NonNull(dagql.NewString(path)), nil
}

func (s *directorySchema) changes(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args struct {
	From core.DirectoryID
}) (res *core.Changeset, _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, err
	}
	dir, err := args.From.Load(ctx, srv)
	if err != nil {
		return res, err
	}
	return core.NewChangeset(ctx, dir, parent)
}

type withChangesArgs struct {
	Changes dagql.ID[*core.Changeset]
	FSDagOpInternalArgs
}

func (s *directorySchema) withChanges(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args withChangesArgs) (res dagql.ObjectResult[*core.Directory], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, err
	}
	changes, err := args.Changes.Load(ctx, srv)
	if err != nil {
		return res, err
	}
	with, err := parent.Self().WithChanges(ctx, changes.Self())
	if err != nil {
		return res, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, with)
}

func (s *directorySchema) changesetLayer(ctx context.Context, parent dagql.ObjectResult[*core.Changeset], args struct {
	FSDagOpInternalArgs
}) (inst dagql.ObjectResult[*core.Directory], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	dir, err := parent.Self().Before.Self().Diff(ctx, parent.Self().After.Self())
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
}

type changesetAsPatchArgs struct {
	FSDagOpInternalArgs
}

func (s *directorySchema) changesetAsPatch(ctx context.Context, parent dagql.ObjectResult[*core.Changeset], _ changesetAsPatchArgs) (inst dagql.ObjectResult[*core.File], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	file, err := parent.Self().AsPatch(ctx)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, file)
}

type changesetExportArgs struct {
	Path string
}

func (s *directorySchema) changesetExport(ctx context.Context, parent *core.Changeset, args changesetExportArgs) (dagql.String, error) {
	err := parent.Export(ctx, args.Path)
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

func (s *directorySchema) changesetEmpty(ctx context.Context, parent dagql.ObjectResult[*core.Changeset], args struct{}) (dagql.Boolean, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return false, err
	}

	var size dagql.Int
	if err := srv.Select(ctx, parent, &size,
		dagql.Selector{
			Field: "asPatch",
		},
		dagql.Selector{
			Field: "size",
		},
	); err != nil {
		return false, err
	}

	return size == 0, nil
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

func getDockerIgnoreFileContent(ctx context.Context, parent dagql.ObjectResult[*core.Directory], filename string) ([]byte, error) {
	file, err := parent.Self().File(ctx, filename)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}

		return nil, err
	}

	content, err := file.Contents(ctx, nil, nil)
	if err != nil {
		return nil, err
	}

	return content, nil
}

func applyDockerIgnore(ctx context.Context, srv *dagql.Server, parent dagql.ObjectResult[*core.Directory], dockerfile string) (dagql.ObjectResult[*core.Directory], error) {
	var buildctxDir dagql.ObjectResult[*core.Directory]

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

func (s *directorySchema) dockerBuild(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args dirDockerBuildArgs) (*core.Container, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	srv, err := query.Server.Server(ctx)
	if err != nil {
		return nil, err
	}

	platform := query.Platform()
	if args.Platform.Valid {
		platform = args.Platform.Value
	}

	buildctxDir, err := applyDockerIgnore(ctx, srv, parent, args.Dockerfile)
	if err != nil {
		return nil, err
	}

	ctr := core.NewContainer(platform)

	secrets, err := dagql.LoadIDResults(ctx, srv, args.Secrets)
	if err != nil {
		return nil, err
	}
	secretStore, err := query.Secrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret store: %w", err)
	}

	return ctr.Build(
		ctx,
		parent.Self(),
		buildctxDir.Self(),
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
	dir dagql.ObjectResult[*core.Directory],
	args directoryTerminalArgs,
) (res dagql.ObjectResult[*core.Directory], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, err
	}

	if len(args.Cmd) == 0 {
		args.Cmd = []string{"sh"}
	}

	var ctr *core.Container

	if args.Container.Valid {
		inst, err := args.Container.Value.Load(ctx, srv)
		if err != nil {
			return res, err
		}
		ctr = inst.Self()
	}

	err = dir.Self().Terminal(ctx, dir.ID(), ctr, &args.TerminalArgs, dir)
	if err != nil {
		return res, err
	}

	return dir, nil
}

func (s *directorySchema) asGit(
	ctx context.Context,
	dir dagql.ObjectResult[*core.Directory],
	_ struct{},
) (inst dagql.Result[*core.GitRepository], _ error) {
	backend := &core.LocalGitRepository{
		Directory: dir,
	}
	repo, err := core.NewGitRepository(ctx, backend)
	if err != nil {
		return inst, err
	}

	inst, err = dagql.NewResultForCurrentID(ctx, repo)
	if err != nil {
		return inst, err
	}
	return inst, nil
}

type directoryWithSymlinkArgs struct {
	Target   string
	LinkName string

	FSDagOpInternalArgs
}

func (s *directorySchema) withSymlink(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args directoryWithSymlinkArgs) (inst dagql.ObjectResult[*core.Directory], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	dir, err := parent.Self().WithSymlink(ctx, srv, args.Target, args.LinkName)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
}

func (s *directorySchema) withError(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args struct{ Err string }) (dagql.ObjectResult[*core.Directory], error) {
	_ = ctx
	if args.Err == "" {
		return parent, nil
	}
	return parent, errors.New(args.Err)
}

type directoryChownArgs struct {
	Path  string
	Owner string

	FSDagOpInternalArgs
}

func (s *directorySchema) chown(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Directory],
	args directoryChownArgs,
) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	dir, err := parent.Self().Chown(ctx, args.Path, args.Owner)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
}

// maintainContentHashing wraps the given directory resolver function and makes the returned directory result content-hashed
// if the parent directory was content-hashed. This allows us to re-use the content-hashing work on the parent for the returned result.
func maintainContentHashing[A any](
	fn dagql.NodeFuncHandler[*core.Directory, A, dagql.ObjectResult[*core.Directory]],
) dagql.NodeFuncHandler[*core.Directory, A, dagql.ObjectResult[*core.Directory]] {
	return func(ctx context.Context, parent dagql.ObjectResult[*core.Directory], args A) (dagql.ObjectResult[*core.Directory], error) {
		res, err := fn(ctx, parent, args)
		if err != nil {
			return res, err
		}

		// in practice right now, a custom digest is always a content hash
		// *unless* it's been manually rewritten using hashutil.HashStrings (e.g. in
		// the case of GitRef.tree - that case is manually rewritten to avoid
		// accidental collisions later)
		if parent.ID().HasCustomDigest() && parent.ID().Digest().Algorithm() == digest.SHA256 {
			query, err := core.CurrentQuery(ctx)
			if err != nil {
				return res, err
			}
			bk, err := query.Buildkit(ctx)
			if err != nil {
				return res, err
			}
			res, err = core.MakeDirectoryContentHashed(ctx, bk, res)
			if err != nil {
				return res, fmt.Errorf("failed to make directory content hashed: %w", err)
			}
			if res.ID().Digest() == parent.ID().Digest() { // if this didn't change anything, return the parent, making this a no-op
				return parent, nil
			}
		}
		return res, nil
	}
}
