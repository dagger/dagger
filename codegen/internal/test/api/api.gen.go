package api

import (
	"context"

	"go.dagger.io/dagger/sdk/go/dagger/querybuilder"
)

func New() *Query {
	return &Query{
		q: querybuilder.Query(),
	}
}

// The address (also known as "ref") of a container published as an OCI image.
//
// Examples:
//   - "alpine"
//   - "index.docker.io/alpine"
//   - "index.docker.io/alpine:latest"
//   - "index.docker.io/alpine:latest@sha256deadbeefdeadbeefdeadbeef"
type ContainerAddress string

func (v ContainerAddress) String() string {
	return string(v)
}

// A unique container identifier. Null designates an empty container (scratch).
type ContainerID string

func (v ContainerID) String() string {
	return string(v)
}

// A content-addressed directory identifier
type DirectoryID string

func (v DirectoryID) String() string {
	return string(v)
}

type FileID string

func (v FileID) String() string {
	return string(v)
}

type SecretID string

func (v SecretID) String() string {
	return string(v)
}

// Additional options for executing a command
type ExecOpts struct {
	// Optionally redirect the command's standard error to a file in the container.
	// Null means discard output.
	RedirectStderr string

	// Optionally redirect the command's standard output to a file in the container.
	// Null means discard output.
	RedirectStdout string

	// Optionally write to the command's standard input
	//
	// - Null means don't touch stdin (no redirection)
	// - Empty string means inject zero bytes to stdin, then send EOF
	Stdin string
}

// An OCI-compatible container, also known as a docker container
type Container struct {
	q *querybuilder.Selection
}

// Retrieve a directory at the given path. Mounts are included.
func (r *Container) Directory(path string) *Directory {
	q := r.q.Select("directory")
	q = q.Arg("path", path)

	return &Directory{
		q: q,
	}
}

// Entrypoint to be prepended to the arguments of all commands
func (r *Container) Entrypoint(ctx context.Context) ([]string, error) {
	q := r.q.Select("entrypoint")

	var response []string
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

// This container after executing the specified command inside it
func (r *Container) Exec(args []string, opts ExecOpts) *Container {
	q := r.q.Select("exec")
	q = q.Arg("args", args)
	q = q.Arg("opts", opts)

	return &Container{
		q: q,
	}
}

// Exit code of the last executed command. Zero means success.
// Null if no command has been executed.
func (r *Container) ExitCode(ctx context.Context) (int, error) {
	q := r.q.Select("exitCode")

	var response int
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

// Initialize this container from the base image published at the given address
func (r *Container) From(address ContainerAddress) *Container {
	q := r.q.Select("from")
	q = q.Arg("address", address)

	return &Container{
		q: q,
	}
}

// A unique identifier for this container
func (r *Container) ID(ctx context.Context) (ContainerID, error) {
	q := r.q.Select("id")

	var response ContainerID
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

// List of paths where a directory is mounted
func (r *Container) Mounts(ctx context.Context) ([]string, error) {
	q := r.q.Select("mounts")

	var response []string
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

// Publish this container as a new image
func (r *Container) Publish(ctx context.Context, address ContainerAddress) (ContainerAddress, error) {
	q := r.q.Select("publish")
	q = q.Arg("address", string(address))

	var response ContainerAddress
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

// This container's root filesystem. Mounts are not included.
func (r *Container) Rootfs() *Directory {
	q := r.q.Select("rootfs")

	return &Directory{
		q: q,
	}
}

// The error stream of the last executed command.
func (r *Container) Stderr() *File {
	q := r.q.Select("stderr")

	return &File{
		q: q,
	}
}

// The output stream of the last executed command.
func (r *Container) Stdout() *File {
	q := r.q.Select("stdout")

	return &File{
		q: q,
	}
}

// The user to be set for all commands
func (r *Container) User(ctx context.Context) (string, error) {
	q := r.q.Select("user")

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

// The value of the specified environment variable
func (r *Container) Variable(ctx context.Context, name string) (string, error) {
	q := r.q.Select("variable")
	q = q.Arg("name", name)

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

// A list of environment variables passed to commands
func (r *Container) Variables(ctx context.Context) ([]string, error) {
	q := r.q.Select("variables")

	var response []string
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

// This container but with a different command entrypoint
func (r *Container) WithEntrypoint(args []string) *Container {
	q := r.q.Select("withEntrypoint")
	q = q.Arg("args", args)

	return &Container{
		q: q,
	}
}

// This container plus a cache directory mounted at the given path
func (r *Container) WithMountedCache(path string, source DirectoryID) *Container {
	q := r.q.Select("withMountedCache")
	q = q.Arg("path", path)
	q = q.Arg("source", source)

	return &Container{
		q: q,
	}
}

// This container plus a directory mounted at the given path
func (r *Container) WithMountedDirectory(path string, source DirectoryID) *Container {
	q := r.q.Select("withMountedDirectory")
	q = q.Arg("path", path)
	q = q.Arg("source", source)

	return &Container{
		q: q,
	}
}

// This container plus a file mounted at the given path
func (r *Container) WithMountedFile(path string, source FileID) *Container {
	q := r.q.Select("withMountedFile")
	q = q.Arg("path", path)
	q = q.Arg("source", source)

	return &Container{
		q: q,
	}
}

// This container plus a secret mounted into a file at the given path
func (r *Container) WithMountedSecret(path string, source SecretID) *Container {
	q := r.q.Select("withMountedSecret")
	q = q.Arg("path", path)
	q = q.Arg("source", source)

	return &Container{
		q: q,
	}
}

// This container plus a temporary directory mounted at the given path
func (r *Container) WithMountedTemp(path string) *Container {
	q := r.q.Select("withMountedTemp")
	q = q.Arg("path", path)

	return &Container{
		q: q,
	}
}

// This container plus an env variable containing the given secret
func (r *Container) WithSecretVariable(secret SecretID, name string) *Container {
	q := r.q.Select("withSecretVariable")
	q = q.Arg("secret", secret)
	q = q.Arg("name", name)

	return &Container{
		q: q,
	}
}

// This container but with a different command user
func (r *Container) WithUser(name string) *Container {
	q := r.q.Select("withUser")
	q = q.Arg("name", name)

	return &Container{
		q: q,
	}
}

// This container plus the given environment variable
func (r *Container) WithVariable(value string, name string) *Container {
	q := r.q.Select("withVariable")
	q = q.Arg("value", value)
	q = q.Arg("name", name)

	return &Container{
		q: q,
	}
}

// This container but with a different working directory
func (r *Container) WithWorkdir(path string) *Container {
	q := r.q.Select("withWorkdir")
	q = q.Arg("path", path)

	return &Container{
		q: q,
	}
}

// This container after unmounting everything at the given path.
func (r *Container) WithoutMount(path string) *Container {
	q := r.q.Select("withoutMount")
	q = q.Arg("path", path)

	return &Container{
		q: q,
	}
}

// This container minus the given environment variable
func (r *Container) WithoutVariable(name string) *Container {
	q := r.q.Select("withoutVariable")
	q = q.Arg("name", name)

	return &Container{
		q: q,
	}
}

// The working directory for all commands
func (r *Container) Workdir(ctx context.Context) (string, error) {
	q := r.q.Select("workdir")

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

// A directory
type Directory struct {
	q *querybuilder.Selection
}

// Return a list of files and directories at the given path
func (r *Directory) Contents(ctx context.Context, path string) ([]string, error) {
	q := r.q.Select("contents")
	q = q.Arg("path", path)

	var response []string
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

// The difference between this directory and an another directory
func (r *Directory) Diff(other DirectoryID) *Directory {
	q := r.q.Select("diff")
	q = q.Arg("other", other)

	return &Directory{
		q: q,
	}
}

// Retrieve a directory at the given path
func (r *Directory) Directory(path string) *Directory {
	q := r.q.Select("directory")
	q = q.Arg("path", path)

	return &Directory{
		q: q,
	}
}

// Retrieve a file at the given path
func (r *Directory) File(path string) *File {
	q := r.q.Select("file")
	q = q.Arg("path", path)

	return &File{
		q: q,
	}
}

// The content-addressed identifier of the directory
func (r *Directory) ID(ctx context.Context) (DirectoryID, error) {
	q := r.q.Select("id")

	var response DirectoryID
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

// A secret backed by the file at the given path
func (r *Directory) Secret(ctx context.Context, path string) (SecretID, error) {
	q := r.q.Select("secret")
	q = q.Arg("path", path)

	var response SecretID
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

// This directory plus the contents of the given file copied to the given path
func (r *Directory) WithCopiedFile(path string, source FileID) *Directory {
	q := r.q.Select("withCopiedFile")
	q = q.Arg("path", path)
	q = q.Arg("source", source)

	return &Directory{
		q: q,
	}
}

// This directory plus a directory written at the given path
func (r *Directory) WithDirectory(path string, directory DirectoryID) *Directory {
	q := r.q.Select("withDirectory")
	q = q.Arg("path", path)
	q = q.Arg("directory", directory)

	return &Directory{
		q: q,
	}
}

// This directory plus a new file written at the given path
func (r *Directory) WithNewFile(path string, contents string) *Directory {
	q := r.q.Select("withNewFile")
	q = q.Arg("path", path)
	q = q.Arg("contents", contents)

	return &Directory{
		q: q,
	}
}

// This directory with the directory at the given path removed
func (r *Directory) WithoutDirectory(path string) *Directory {
	q := r.q.Select("withoutDirectory")
	q = q.Arg("path", path)

	return &Directory{
		q: q,
	}
}

// This directory with the file at the given path removed
func (r *Directory) WithoutFile(path string) *Directory {
	q := r.q.Select("withoutFile")
	q = q.Arg("path", path)

	return &Directory{
		q: q,
	}
}

// A file
type File struct {
	q *querybuilder.Selection
}

// The contents of the file
func (r *File) Contents(ctx context.Context) (string, error) {
	q := r.q.Select("contents")

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

// The content-addressed identifier of the file
func (r *File) ID(ctx context.Context) (FileID, error) {
	q := r.q.Select("id")

	var response FileID
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

// The size of the file, in bytes
func (r *File) Size(ctx context.Context) (int, error) {
	q := r.q.Select("size")

	var response int
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

// A git ref (tag or branch)
type GitRef struct {
	q *querybuilder.Selection
}

// The digest of the current value of this ref
func (r *GitRef) Digest(ctx context.Context) (string, error) {
	q := r.q.Select("digest")

	var response string
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

// The filesystem tree at this ref
func (r *GitRef) Tree() *Directory {
	q := r.q.Select("tree")

	return &Directory{
		q: q,
	}
}

// A git repository
type GitRepository struct {
	q *querybuilder.Selection
}

// Details on one branch
func (r *GitRepository) Branch(name string) *GitRef {
	q := r.q.Select("branch")
	q = q.Arg("name", name)

	return &GitRef{
		q: q,
	}
}

// List of branches on the repository
func (r *GitRepository) Branches(ctx context.Context) ([]string, error) {
	q := r.q.Select("branches")

	var response []string
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

// Details on one tag
func (r *GitRepository) Tag(name string) *GitRef {
	q := r.q.Select("tag")
	q = q.Arg("name", name)

	return &GitRef{
		q: q,
	}
}

// List of tags on the repository
func (r *GitRepository) Tags(ctx context.Context) ([]string, error) {
	q := r.q.Select("tags")

	var response []string
	q = q.Bind(&response)
	return response, q.Execute(ctx)
}

type Query struct {
	q *querybuilder.Selection
}

// Load a container from ID.
// Null ID returns an empty container (scratch).
func (r *Query) Container(id ContainerID) *Container {
	q := r.q.Select("container")
	q = q.Arg("id", id)

	return &Container{
		q: q,
	}
}

// Load a directory by ID. No argument produces an empty directory.
func (r *Query) Directory(id DirectoryID) *Directory {
	q := r.q.Select("directory")
	q = q.Arg("id", id)

	return &Directory{
		q: q,
	}
}

// Load a file by ID
func (r *Query) File(id FileID) *File {
	q := r.q.Select("file")
	q = q.Arg("id", id)

	return &File{
		q: q,
	}
}

// Query a git repository
func (r *Query) Git(url string) *GitRepository {
	q := r.q.Select("git")
	q = q.Arg("url", url)

	return &GitRepository{
		q: q,
	}
}
