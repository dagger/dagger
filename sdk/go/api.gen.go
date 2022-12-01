// Code generated by dagger. DO NOT EDIT.

package dagger

import (
	"context"

	"dagger.io/dagger/internal/querybuilder"
	"github.com/Khan/genqlient/graphql"
)

// A global cache volume identifier
type CacheID string

// A unique container identifier. Null designates an empty container (scratch).
type ContainerID string

// A content-addressed directory identifier
type DirectoryID string

type FileID string

type Platform string

// A unique identifier for a secret
type SecretID string

// A content-addressed socket identifier
type SocketID string

// A directory whose contents persist across runs
type CacheVolume struct {
	q *querybuilder.Selection
	c graphql.Client
}

func (r *CacheVolume) ID(ctx context.Context) (CacheID, error) {
	q := r.q.Select("id")

	var response CacheID
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// XXX_GraphQLType is an internal function. It returns the native GraphQL type name
func (r *CacheVolume) XXX_GraphQLType() string {
	return "CacheVolume"
}

// XXX_GraphQLID is an internal function. It returns the underlying type ID
func (r *CacheVolume) XXX_GraphQLID(ctx context.Context) (string, error) {
	id, err := r.ID(ctx)
	if err != nil {
		return "", err
	}
	return string(id), nil
}

// An OCI-compatible container, also known as a docker container
type Container struct {
	q *querybuilder.Selection
	c graphql.Client
}

// ContainerBuildOpts contains options for Container.Build
type ContainerBuildOpts struct {
	Dockerfile string
}

// Initialize this container from a Dockerfile build
func (r *Container) Build(context *Directory, opts ...ContainerBuildOpts) *Container {
	q := r.q.Select("build")
	q = q.Arg("context", context)
	// `dockerfile` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Dockerfile) {
			q = q.Arg("dockerfile", opts[i].Dockerfile)
			break
		}
	}

	return &Container{
		q: q,
		c: r.c,
	}
}

// Default arguments for future commands
func (r *Container) DefaultArgs(ctx context.Context) ([]string, error) {
	q := r.q.Select("defaultArgs")

	var response []string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// Retrieve a directory at the given path. Mounts are included.
func (r *Container) Directory(path string) *Directory {
	q := r.q.Select("directory")
	q = q.Arg("path", path)

	return &Directory{
		q: q,
		c: r.c,
	}
}

// Entrypoint to be prepended to the arguments of all commands
func (r *Container) Entrypoint(ctx context.Context) ([]string, error) {
	q := r.q.Select("entrypoint")

	var response []string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// The value of the specified environment variable
func (r *Container) EnvVariable(ctx context.Context, name string) (string, error) {
	q := r.q.Select("envVariable")
	q = q.Arg("name", name)

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// A list of environment variables passed to commands
func (r *Container) EnvVariables(ctx context.Context) ([]EnvVariable, error) {
	q := r.q.Select("envVariables")

	var response []EnvVariable
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// ContainerExecOpts contains options for Container.Exec
type ContainerExecOpts struct {
	// Command to run instead of the container's default command
	Args []string
	// Content to write to the command's standard input before closing
	Stdin string
	// Redirect the command's standard output to a file in the container
	RedirectStdout string
	// Redirect the command's standard error to a file in the container
	RedirectStderr string
	// Provide dagger access to the executed command
	// Do not use this option unless you trust the command being executed
	// The command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM
	ExperimentalPrivilegedNesting bool
}

// This container after executing the specified command inside it
//
// Deprecated: Replaced by WithExec.
func (r *Container) Exec(opts ...ContainerExecOpts) *Container {
	q := r.q.Select("exec")
	// `args` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Args) {
			q = q.Arg("args", opts[i].Args)
			break
		}
	}
	// `stdin` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Stdin) {
			q = q.Arg("stdin", opts[i].Stdin)
			break
		}
	}
	// `redirectStdout` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].RedirectStdout) {
			q = q.Arg("redirectStdout", opts[i].RedirectStdout)
			break
		}
	}
	// `redirectStderr` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].RedirectStderr) {
			q = q.Arg("redirectStderr", opts[i].RedirectStderr)
			break
		}
	}
	// `experimentalPrivilegedNesting` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].ExperimentalPrivilegedNesting) {
			q = q.Arg("experimentalPrivilegedNesting", opts[i].ExperimentalPrivilegedNesting)
			break
		}
	}

	return &Container{
		q: q,
		c: r.c,
	}
}

// Exit code of the last executed command. Zero means success.
// Null if no command has been executed.
func (r *Container) ExitCode(ctx context.Context) (int, error) {
	q := r.q.Select("exitCode")

	var response int
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// ContainerExportOpts contains options for Container.Export
type ContainerExportOpts struct {
	PlatformVariants []*Container
}

// Write the container as an OCI tarball to the destination file path on the host
func (r *Container) Export(ctx context.Context, path string, opts ...ContainerExportOpts) (bool, error) {
	q := r.q.Select("export")
	q = q.Arg("path", path)
	// `platformVariants` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].PlatformVariants) {
			q = q.Arg("platformVariants", opts[i].PlatformVariants)
			break
		}
	}

	var response bool
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// Retrieve a file at the given path. Mounts are included.
func (r *Container) File(path string) *File {
	q := r.q.Select("file")
	q = q.Arg("path", path)

	return &File{
		q: q,
		c: r.c,
	}
}

// Initialize this container from the base image published at the given address
func (r *Container) From(address string) *Container {
	q := r.q.Select("from")
	q = q.Arg("address", address)

	return &Container{
		q: q,
		c: r.c,
	}
}

// This container's root filesystem. Mounts are not included.
//
// Deprecated: Replaced by Rootfs.
func (r *Container) FS() *Directory {
	q := r.q.Select("fs")

	return &Directory{
		q: q,
		c: r.c,
	}
}

// A unique identifier for this container
func (r *Container) ID(ctx context.Context) (ContainerID, error) {
	q := r.q.Select("id")

	var response ContainerID
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// XXX_GraphQLType is an internal function. It returns the native GraphQL type name
func (r *Container) XXX_GraphQLType() string {
	return "Container"
}

// XXX_GraphQLID is an internal function. It returns the underlying type ID
func (r *Container) XXX_GraphQLID(ctx context.Context) (string, error) {
	id, err := r.ID(ctx)
	if err != nil {
		return "", err
	}
	return string(id), nil
}

// List of paths where a directory is mounted
func (r *Container) Mounts(ctx context.Context) ([]string, error) {
	q := r.q.Select("mounts")

	var response []string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// The platform this container executes and publishes as
func (r *Container) Platform(ctx context.Context) (Platform, error) {
	q := r.q.Select("platform")

	var response Platform
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// ContainerPublishOpts contains options for Container.Publish
type ContainerPublishOpts struct {
	PlatformVariants []*Container
}

// Publish this container as a new image, returning a fully qualified ref
func (r *Container) Publish(ctx context.Context, address string, opts ...ContainerPublishOpts) (string, error) {
	q := r.q.Select("publish")
	q = q.Arg("address", address)
	// `platformVariants` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].PlatformVariants) {
			q = q.Arg("platformVariants", opts[i].PlatformVariants)
			break
		}
	}

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// This container's root filesystem. Mounts are not included.
func (r *Container) Rootfs() *Directory {
	q := r.q.Select("rootfs")

	return &Directory{
		q: q,
		c: r.c,
	}
}

// The error stream of the last executed command.
// Null if no command has been executed.
func (r *Container) Stderr(ctx context.Context) (string, error) {
	q := r.q.Select("stderr")

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// The output stream of the last executed command.
// Null if no command has been executed.
func (r *Container) Stdout(ctx context.Context) (string, error) {
	q := r.q.Select("stdout")

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// The user to be set for all commands
func (r *Container) User(ctx context.Context) (string, error) {
	q := r.q.Select("user")

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// ContainerWithDefaultArgsOpts contains options for Container.WithDefaultArgs
type ContainerWithDefaultArgsOpts struct {
	Args []string
}

// Configures default arguments for future commands
func (r *Container) WithDefaultArgs(opts ...ContainerWithDefaultArgsOpts) *Container {
	q := r.q.Select("withDefaultArgs")
	// `args` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Args) {
			q = q.Arg("args", opts[i].Args)
			break
		}
	}

	return &Container{
		q: q,
		c: r.c,
	}
}

// ContainerWithDirectoryOpts contains options for Container.WithDirectory
type ContainerWithDirectoryOpts struct {
	Exclude []string

	Include []string
}

// This container plus a directory written at the given path
func (r *Container) WithDirectory(path string, directory *Directory, opts ...ContainerWithDirectoryOpts) *Container {
	q := r.q.Select("withDirectory")
	q = q.Arg("path", path)
	q = q.Arg("directory", directory)
	// `exclude` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Exclude) {
			q = q.Arg("exclude", opts[i].Exclude)
			break
		}
	}
	// `include` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Include) {
			q = q.Arg("include", opts[i].Include)
			break
		}
	}

	return &Container{
		q: q,
		c: r.c,
	}
}

// This container but with a different command entrypoint
func (r *Container) WithEntrypoint(args []string) *Container {
	q := r.q.Select("withEntrypoint")
	q = q.Arg("args", args)

	return &Container{
		q: q,
		c: r.c,
	}
}

// This container plus the given environment variable
func (r *Container) WithEnvVariable(name string, value string) *Container {
	q := r.q.Select("withEnvVariable")
	q = q.Arg("name", name)
	q = q.Arg("value", value)

	return &Container{
		q: q,
		c: r.c,
	}
}

// ContainerWithExecOpts contains options for Container.WithExec
type ContainerWithExecOpts struct {
	// Content to write to the command's standard input before closing
	Stdin string
	// Redirect the command's standard output to a file in the container
	RedirectStdout string
	// Redirect the command's standard error to a file in the container
	RedirectStderr string
	// Provide dagger access to the executed command
	// Do not use this option unless you trust the command being executed
	// The command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM
	ExperimentalPrivilegedNesting bool
}

// This container after executing the specified command inside it
func (r *Container) WithExec(args []string, opts ...ContainerWithExecOpts) *Container {
	q := r.q.Select("withExec")
	q = q.Arg("args", args)
	// `stdin` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Stdin) {
			q = q.Arg("stdin", opts[i].Stdin)
			break
		}
	}
	// `redirectStdout` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].RedirectStdout) {
			q = q.Arg("redirectStdout", opts[i].RedirectStdout)
			break
		}
	}
	// `redirectStderr` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].RedirectStderr) {
			q = q.Arg("redirectStderr", opts[i].RedirectStderr)
			break
		}
	}
	// `experimentalPrivilegedNesting` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].ExperimentalPrivilegedNesting) {
			q = q.Arg("experimentalPrivilegedNesting", opts[i].ExperimentalPrivilegedNesting)
			break
		}
	}

	return &Container{
		q: q,
		c: r.c,
	}
}

// Initialize this container from this DirectoryID
//
// Deprecated: Replaced by WithRootfs.
func (r *Container) WithFS(id *Directory) *Container {
	q := r.q.Select("withFS")
	q = q.Arg("id", id)

	return &Container{
		q: q,
		c: r.c,
	}
}

// This container plus the contents of the given file copied to the given path
func (r *Container) WithFile(path string, source *File) *Container {
	q := r.q.Select("withFile")
	q = q.Arg("path", path)
	q = q.Arg("source", source)

	return &Container{
		q: q,
		c: r.c,
	}
}

// ContainerWithMountedCacheOpts contains options for Container.WithMountedCache
type ContainerWithMountedCacheOpts struct {
	Source *Directory
}

// This container plus a cache volume mounted at the given path
func (r *Container) WithMountedCache(path string, cache *CacheVolume, opts ...ContainerWithMountedCacheOpts) *Container {
	q := r.q.Select("withMountedCache")
	q = q.Arg("path", path)
	q = q.Arg("cache", cache)
	// `source` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Source) {
			q = q.Arg("source", opts[i].Source)
			break
		}
	}

	return &Container{
		q: q,
		c: r.c,
	}
}

// This container plus a directory mounted at the given path
func (r *Container) WithMountedDirectory(path string, source *Directory) *Container {
	q := r.q.Select("withMountedDirectory")
	q = q.Arg("path", path)
	q = q.Arg("source", source)

	return &Container{
		q: q,
		c: r.c,
	}
}

// This container plus a file mounted at the given path
func (r *Container) WithMountedFile(path string, source *File) *Container {
	q := r.q.Select("withMountedFile")
	q = q.Arg("path", path)
	q = q.Arg("source", source)

	return &Container{
		q: q,
		c: r.c,
	}
}

// This container plus a secret mounted into a file at the given path
func (r *Container) WithMountedSecret(path string, source *Secret) *Container {
	q := r.q.Select("withMountedSecret")
	q = q.Arg("path", path)
	q = q.Arg("source", source)

	return &Container{
		q: q,
		c: r.c,
	}
}

// This container plus a temporary directory mounted at the given path
func (r *Container) WithMountedTemp(path string) *Container {
	q := r.q.Select("withMountedTemp")
	q = q.Arg("path", path)

	return &Container{
		q: q,
		c: r.c,
	}
}

// ContainerWithNewFileOpts contains options for Container.WithNewFile
type ContainerWithNewFileOpts struct {
	Contents string
}

// This container plus a new file written at the given path
func (r *Container) WithNewFile(path string, opts ...ContainerWithNewFileOpts) *Container {
	q := r.q.Select("withNewFile")
	q = q.Arg("path", path)
	// `contents` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Contents) {
			q = q.Arg("contents", opts[i].Contents)
			break
		}
	}

	return &Container{
		q: q,
		c: r.c,
	}
}

// Initialize this container from this DirectoryID
func (r *Container) WithRootfs(id *Directory) *Container {
	q := r.q.Select("withRootfs")
	q = q.Arg("id", id)

	return &Container{
		q: q,
		c: r.c,
	}
}

// This container plus an env variable containing the given secret
func (r *Container) WithSecretVariable(name string, secret *Secret) *Container {
	q := r.q.Select("withSecretVariable")
	q = q.Arg("name", name)
	q = q.Arg("secret", secret)

	return &Container{
		q: q,
		c: r.c,
	}
}

// This container plus a socket forwarded to the given Unix socket path
func (r *Container) WithUnixSocket(path string, source *Socket) *Container {
	q := r.q.Select("withUnixSocket")
	q = q.Arg("path", path)
	q = q.Arg("source", source)

	return &Container{
		q: q,
		c: r.c,
	}
}

// This container but with a different command user
func (r *Container) WithUser(name string) *Container {
	q := r.q.Select("withUser")
	q = q.Arg("name", name)

	return &Container{
		q: q,
		c: r.c,
	}
}

// This container but with a different working directory
func (r *Container) WithWorkdir(path string) *Container {
	q := r.q.Select("withWorkdir")
	q = q.Arg("path", path)

	return &Container{
		q: q,
		c: r.c,
	}
}

// This container minus the given environment variable
func (r *Container) WithoutEnvVariable(name string) *Container {
	q := r.q.Select("withoutEnvVariable")
	q = q.Arg("name", name)

	return &Container{
		q: q,
		c: r.c,
	}
}

// This container after unmounting everything at the given path.
func (r *Container) WithoutMount(path string) *Container {
	q := r.q.Select("withoutMount")
	q = q.Arg("path", path)

	return &Container{
		q: q,
		c: r.c,
	}
}

// This container with a previously added Unix socket removed
func (r *Container) WithoutUnixSocket(path string) *Container {
	q := r.q.Select("withoutUnixSocket")
	q = q.Arg("path", path)

	return &Container{
		q: q,
		c: r.c,
	}
}

// The working directory for all commands
func (r *Container) Workdir(ctx context.Context) (string, error) {
	q := r.q.Select("workdir")

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// A directory
type Directory struct {
	q *querybuilder.Selection
	c graphql.Client
}

// The difference between this directory and an another directory
func (r *Directory) Diff(other *Directory) *Directory {
	q := r.q.Select("diff")
	q = q.Arg("other", other)

	return &Directory{
		q: q,
		c: r.c,
	}
}

// Retrieve a directory at the given path
func (r *Directory) Directory(path string) *Directory {
	q := r.q.Select("directory")
	q = q.Arg("path", path)

	return &Directory{
		q: q,
		c: r.c,
	}
}

// DirectoryDockerBuildOpts contains options for Directory.DockerBuild
type DirectoryDockerBuildOpts struct {
	Dockerfile string

	Platform Platform
}

// Build a new Docker container from this directory
func (r *Directory) DockerBuild(opts ...DirectoryDockerBuildOpts) *Container {
	q := r.q.Select("dockerBuild")
	// `dockerfile` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Dockerfile) {
			q = q.Arg("dockerfile", opts[i].Dockerfile)
			break
		}
	}
	// `platform` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Platform) {
			q = q.Arg("platform", opts[i].Platform)
			break
		}
	}

	return &Container{
		q: q,
		c: r.c,
	}
}

// DirectoryEntriesOpts contains options for Directory.Entries
type DirectoryEntriesOpts struct {
	Path string
}

// Return a list of files and directories at the given path
func (r *Directory) Entries(ctx context.Context, opts ...DirectoryEntriesOpts) ([]string, error) {
	q := r.q.Select("entries")
	// `path` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Path) {
			q = q.Arg("path", opts[i].Path)
			break
		}
	}

	var response []string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// Write the contents of the directory to a path on the host
func (r *Directory) Export(ctx context.Context, path string) (bool, error) {
	q := r.q.Select("export")
	q = q.Arg("path", path)

	var response bool
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// Retrieve a file at the given path
func (r *Directory) File(path string) *File {
	q := r.q.Select("file")
	q = q.Arg("path", path)

	return &File{
		q: q,
		c: r.c,
	}
}

// The content-addressed identifier of the directory
func (r *Directory) ID(ctx context.Context) (DirectoryID, error) {
	q := r.q.Select("id")

	var response DirectoryID
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// XXX_GraphQLType is an internal function. It returns the native GraphQL type name
func (r *Directory) XXX_GraphQLType() string {
	return "Directory"
}

// XXX_GraphQLID is an internal function. It returns the underlying type ID
func (r *Directory) XXX_GraphQLID(ctx context.Context) (string, error) {
	id, err := r.ID(ctx)
	if err != nil {
		return "", err
	}
	return string(id), nil
}

// load a project's metadata
func (r *Directory) LoadProject(configPath string) *Project {
	q := r.q.Select("loadProject")
	q = q.Arg("configPath", configPath)

	return &Project{
		q: q,
		c: r.c,
	}
}

// DirectoryWithDirectoryOpts contains options for Directory.WithDirectory
type DirectoryWithDirectoryOpts struct {
	Exclude []string

	Include []string
}

// This directory plus a directory written at the given path
func (r *Directory) WithDirectory(path string, directory *Directory, opts ...DirectoryWithDirectoryOpts) *Directory {
	q := r.q.Select("withDirectory")
	q = q.Arg("path", path)
	q = q.Arg("directory", directory)
	// `exclude` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Exclude) {
			q = q.Arg("exclude", opts[i].Exclude)
			break
		}
	}
	// `include` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Include) {
			q = q.Arg("include", opts[i].Include)
			break
		}
	}

	return &Directory{
		q: q,
		c: r.c,
	}
}

// This directory plus the contents of the given file copied to the given path
func (r *Directory) WithFile(path string, source *File) *Directory {
	q := r.q.Select("withFile")
	q = q.Arg("path", path)
	q = q.Arg("source", source)

	return &Directory{
		q: q,
		c: r.c,
	}
}

// This directory plus a new directory created at the given path
func (r *Directory) WithNewDirectory(path string) *Directory {
	q := r.q.Select("withNewDirectory")
	q = q.Arg("path", path)

	return &Directory{
		q: q,
		c: r.c,
	}
}

// This directory plus a new file written at the given path
func (r *Directory) WithNewFile(path string, contents string) *Directory {
	q := r.q.Select("withNewFile")
	q = q.Arg("path", path)
	q = q.Arg("contents", contents)

	return &Directory{
		q: q,
		c: r.c,
	}
}

// This directory with the directory at the given path removed
func (r *Directory) WithoutDirectory(path string) *Directory {
	q := r.q.Select("withoutDirectory")
	q = q.Arg("path", path)

	return &Directory{
		q: q,
		c: r.c,
	}
}

// This directory with the file at the given path removed
func (r *Directory) WithoutFile(path string) *Directory {
	q := r.q.Select("withoutFile")
	q = q.Arg("path", path)

	return &Directory{
		q: q,
		c: r.c,
	}
}

// EnvVariable is a simple key value object that represents an environment variable.
type EnvVariable struct {
	q *querybuilder.Selection
	c graphql.Client
}

// name is the environment variable name.
func (r *EnvVariable) Name(ctx context.Context) (string, error) {
	q := r.q.Select("name")

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// value is the environment variable value
func (r *EnvVariable) Value(ctx context.Context) (string, error) {
	q := r.q.Select("value")

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// A file
type File struct {
	q *querybuilder.Selection
	c graphql.Client
}

// The contents of the file
func (r *File) Contents(ctx context.Context) (string, error) {
	q := r.q.Select("contents")

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// Write the file to a file path on the host
func (r *File) Export(ctx context.Context, path string) (bool, error) {
	q := r.q.Select("export")
	q = q.Arg("path", path)

	var response bool
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// The content-addressed identifier of the file
func (r *File) ID(ctx context.Context) (FileID, error) {
	q := r.q.Select("id")

	var response FileID
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// XXX_GraphQLType is an internal function. It returns the native GraphQL type name
func (r *File) XXX_GraphQLType() string {
	return "File"
}

// XXX_GraphQLID is an internal function. It returns the underlying type ID
func (r *File) XXX_GraphQLID(ctx context.Context) (string, error) {
	id, err := r.ID(ctx)
	if err != nil {
		return "", err
	}
	return string(id), nil
}

func (r *File) Secret() *Secret {
	q := r.q.Select("secret")

	return &Secret{
		q: q,
		c: r.c,
	}
}

// The size of the file, in bytes
func (r *File) Size(ctx context.Context) (int, error) {
	q := r.q.Select("size")

	var response int
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// A git ref (tag or branch)
type GitRef struct {
	q *querybuilder.Selection
	c graphql.Client
}

// The digest of the current value of this ref
func (r *GitRef) Digest(ctx context.Context) (string, error) {
	q := r.q.Select("digest")

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// GitRefTreeOpts contains options for GitRef.Tree
type GitRefTreeOpts struct {
	SSHKnownHosts string

	SSHAuthSocket *Socket
}

// The filesystem tree at this ref
func (r *GitRef) Tree(opts ...GitRefTreeOpts) *Directory {
	q := r.q.Select("tree")
	// `sshKnownHosts` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].SSHKnownHosts) {
			q = q.Arg("sshKnownHosts", opts[i].SSHKnownHosts)
			break
		}
	}
	// `sshAuthSocket` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].SSHAuthSocket) {
			q = q.Arg("sshAuthSocket", opts[i].SSHAuthSocket)
			break
		}
	}

	return &Directory{
		q: q,
		c: r.c,
	}
}

// A git repository
type GitRepository struct {
	q *querybuilder.Selection
	c graphql.Client
}

// Details on one branch
func (r *GitRepository) Branch(name string) *GitRef {
	q := r.q.Select("branch")
	q = q.Arg("name", name)

	return &GitRef{
		q: q,
		c: r.c,
	}
}

// List of branches on the repository
func (r *GitRepository) Branches(ctx context.Context) ([]string, error) {
	q := r.q.Select("branches")

	var response []string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// Details on one commit
func (r *GitRepository) Commit(id string) *GitRef {
	q := r.q.Select("commit")
	q = q.Arg("id", id)

	return &GitRef{
		q: q,
		c: r.c,
	}
}

// Details on one tag
func (r *GitRepository) Tag(name string) *GitRef {
	q := r.q.Select("tag")
	q = q.Arg("name", name)

	return &GitRef{
		q: q,
		c: r.c,
	}
}

// List of tags on the repository
func (r *GitRepository) Tags(ctx context.Context) ([]string, error) {
	q := r.q.Select("tags")

	var response []string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// Information about the host execution environment
type Host struct {
	q *querybuilder.Selection
	c graphql.Client
}

// HostDirectoryOpts contains options for Host.Directory
type HostDirectoryOpts struct {
	Exclude []string

	Include []string
}

// Access a directory on the host
func (r *Host) Directory(path string, opts ...HostDirectoryOpts) *Directory {
	q := r.q.Select("directory")
	q = q.Arg("path", path)
	// `exclude` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Exclude) {
			q = q.Arg("exclude", opts[i].Exclude)
			break
		}
	}
	// `include` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Include) {
			q = q.Arg("include", opts[i].Include)
			break
		}
	}

	return &Directory{
		q: q,
		c: r.c,
	}
}

// Access an environment variable on the host
func (r *Host) EnvVariable(name string) *HostVariable {
	q := r.q.Select("envVariable")
	q = q.Arg("name", name)

	return &HostVariable{
		q: q,
		c: r.c,
	}
}

// Access a TCP host:port address via the host network
func (r *Host) TCPSocket(address string) *Socket {
	q := r.q.Select("tcpSocket")
	q = q.Arg("address", address)

	return &Socket{
		q: q,
		c: r.c,
	}
}

// Access a UDP host:port address via the host network
func (r *Host) UDPSocket(address string) *Socket {
	q := r.q.Select("udpSocket")
	q = q.Arg("address", address)

	return &Socket{
		q: q,
		c: r.c,
	}
}

// Access a Unix socket on the host
func (r *Host) UnixSocket(path string) *Socket {
	q := r.q.Select("unixSocket")
	q = q.Arg("path", path)

	return &Socket{
		q: q,
		c: r.c,
	}
}

// HostWorkdirOpts contains options for Host.Workdir
type HostWorkdirOpts struct {
	Exclude []string

	Include []string
}

// The current working directory on the host
//
// Deprecated: Use Directory with path set to '.' instead.
func (r *Host) Workdir(opts ...HostWorkdirOpts) *Directory {
	q := r.q.Select("workdir")
	// `exclude` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Exclude) {
			q = q.Arg("exclude", opts[i].Exclude)
			break
		}
	}
	// `include` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Include) {
			q = q.Arg("include", opts[i].Include)
			break
		}
	}

	return &Directory{
		q: q,
		c: r.c,
	}
}

// An environment variable on the host environment
type HostVariable struct {
	q *querybuilder.Selection
	c graphql.Client
}

// A secret referencing the value of this variable
func (r *HostVariable) Secret() *Secret {
	q := r.q.Select("secret")

	return &Secret{
		q: q,
		c: r.c,
	}
}

// The value of this variable
func (r *HostVariable) Value(ctx context.Context) (string, error) {
	q := r.q.Select("value")

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// A set of scripts and/or extensions
type Project struct {
	q *querybuilder.Selection
	c graphql.Client
}

// extensions in this project
func (r *Project) Extensions(ctx context.Context) ([]Project, error) {
	q := r.q.Select("extensions")

	var response []Project
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// Code files generated by the SDKs in the project
func (r *Project) GeneratedCode() *Directory {
	q := r.q.Select("generatedCode")

	return &Directory{
		q: q,
		c: r.c,
	}
}

// install the project's schema
func (r *Project) Install(ctx context.Context) (bool, error) {
	q := r.q.Select("install")

	var response bool
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// name of the project
func (r *Project) Name(ctx context.Context) (string, error) {
	q := r.q.Select("name")

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// schema provided by the project
func (r *Project) Schema(ctx context.Context) (string, error) {
	q := r.q.Select("schema")

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// sdk used to generate code for and/or execute this project
func (r *Project) SDK(ctx context.Context) (string, error) {
	q := r.q.Select("sdk")

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

type Query struct {
	q *querybuilder.Selection
	c graphql.Client
}

// Construct a cache volume for a given cache key
func (r *Query) CacheVolume(key string) *CacheVolume {
	q := r.q.Select("cacheVolume")
	q = q.Arg("key", key)

	return &CacheVolume{
		q: q,
		c: r.c,
	}
}

// ContainerOpts contains options for Query.Container
type ContainerOpts struct {
	ID ContainerID

	Platform Platform
}

// Load a container from ID.
// Null ID returns an empty container (scratch).
// Optional platform argument initializes new containers to execute and publish as that platform. Platform defaults to that of the builder's host.
func (r *Query) Container(opts ...ContainerOpts) *Container {
	q := r.q.Select("container")
	// `id` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].ID) {
			q = q.Arg("id", opts[i].ID)
			break
		}
	}
	// `platform` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].Platform) {
			q = q.Arg("platform", opts[i].Platform)
			break
		}
	}

	return &Container{
		q: q,
		c: r.c,
	}
}

// The default platform of the builder.
func (r *Query) DefaultPlatform(ctx context.Context) (Platform, error) {
	q := r.q.Select("defaultPlatform")

	var response Platform
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// DirectoryOpts contains options for Query.Directory
type DirectoryOpts struct {
	ID DirectoryID
}

// Load a directory by ID. No argument produces an empty directory.
func (r *Query) Directory(opts ...DirectoryOpts) *Directory {
	q := r.q.Select("directory")
	// `id` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].ID) {
			q = q.Arg("id", opts[i].ID)
			break
		}
	}

	return &Directory{
		q: q,
		c: r.c,
	}
}

// Load a file by ID
func (r *Query) File(id FileID) *File {
	q := r.q.Select("file")
	q = q.Arg("id", id)

	return &File{
		q: q,
		c: r.c,
	}
}

// GitOpts contains options for Query.Git
type GitOpts struct {
	KeepGitDir bool
}

// Query a git repository
func (r *Query) Git(url string, opts ...GitOpts) *GitRepository {
	q := r.q.Select("git")
	q = q.Arg("url", url)
	// `keepGitDir` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].KeepGitDir) {
			q = q.Arg("keepGitDir", opts[i].KeepGitDir)
			break
		}
	}

	return &GitRepository{
		q: q,
		c: r.c,
	}
}

// Query the host environment
func (r *Query) Host() *Host {
	q := r.q.Select("host")

	return &Host{
		q: q,
		c: r.c,
	}
}

// An http remote
func (r *Query) HTTP(url string) *File {
	q := r.q.Select("http")
	q = q.Arg("url", url)

	return &File{
		q: q,
		c: r.c,
	}
}

// Look up a project by name
func (r *Query) Project(name string) *Project {
	q := r.q.Select("project")
	q = q.Arg("name", name)

	return &Project{
		q: q,
		c: r.c,
	}
}

// Load a secret from its ID
func (r *Query) Secret(id SecretID) *Secret {
	q := r.q.Select("secret")
	q = q.Arg("id", id)

	return &Secret{
		q: q,
		c: r.c,
	}
}

// SocketOpts contains options for Query.Socket
type SocketOpts struct {
	ID SocketID
}

// Load a socket by ID
func (r *Query) Socket(opts ...SocketOpts) *Socket {
	q := r.q.Select("socket")
	// `id` optional argument
	for i := len(opts) - 1; i >= 0; i-- {
		if !querybuilder.IsZeroValue(opts[i].ID) {
			q = q.Arg("id", opts[i].ID)
			break
		}
	}

	return &Socket{
		q: q,
		c: r.c,
	}
}

// A reference to a secret value, which can be handled more safely than the value itself
type Secret struct {
	q *querybuilder.Selection
	c graphql.Client
}

// The identifier for this secret
func (r *Secret) ID(ctx context.Context) (SecretID, error) {
	q := r.q.Select("id")

	var response SecretID
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// XXX_GraphQLType is an internal function. It returns the native GraphQL type name
func (r *Secret) XXX_GraphQLType() string {
	return "Secret"
}

// XXX_GraphQLID is an internal function. It returns the underlying type ID
func (r *Secret) XXX_GraphQLID(ctx context.Context) (string, error) {
	id, err := r.ID(ctx)
	if err != nil {
		return "", err
	}
	return string(id), nil
}

// The value of this secret
func (r *Secret) Plaintext(ctx context.Context) (string, error) {
	q := r.q.Select("plaintext")

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

type Socket struct {
	q *querybuilder.Selection
	c graphql.Client
}

// The content-addressed identifier of the socket
func (r *Socket) ID(ctx context.Context) (SocketID, error) {
	q := r.q.Select("id")

	var response SocketID
	q = q.Bind(&response)
	return response, q.Execute(ctx, r.c)
}

// XXX_GraphQLType is an internal function. It returns the native GraphQL type name
func (r *Socket) XXX_GraphQLType() string {
	return "Socket"
}

// XXX_GraphQLID is an internal function. It returns the underlying type ID
func (r *Socket) XXX_GraphQLID(ctx context.Context) (string, error) {
	id, err := r.ID(ctx)
	if err != nil {
		return "", err
	}
	return string(id), nil
}
