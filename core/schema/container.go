package schema

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/moby/buildkit/frontend/dockerfile/shell"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/slog"
)

type containerSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &containerSchema{}

func (s *containerSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("container", s.container).
			Doc(`Creates a scratch container.`,
				`Optional platform argument initializes new containers to execute and
				publish as that platform. Platform defaults to that of the builder's
				host.`).
			ArgDoc("platform", `Platform to initialize the container with.`).
			ArgDeprecated("id", "Use `loadContainerFromID` instead."),
	}.Install(s.srv)

	dagql.Fields[*core.Container]{
		Syncer[*core.Container]().
			Doc(`Forces evaluation of the pipeline in the engine.`,
				`It doesn't run the default command if no exec has been set.`),

		dagql.Func("pipeline", s.pipeline).
			Doc(`Creates a named sub-pipeline.`).
			ArgDoc("name", "Name of the sub-pipeline.").
			ArgDoc("description", "Description of the sub-pipeline.").
			ArgDoc("labels", "Labels to apply to the sub-pipeline."),

		dagql.Func("from", s.from).
			Doc(`Initializes this container from a pulled base image.`).
			ArgDoc("address",
				`Image's address from its registry.`,
				`Formatted as [host]/[user]/[repo]:[tag] (e.g., "docker.io/dagger/dagger:main").`),

		dagql.Func("build", s.build).
			Doc(`Initializes this container from a Dockerfile build.`).
			ArgDoc("context", "Directory context used by the Dockerfile.").
			ArgDoc("dockerfile", "Path to the Dockerfile to use.").
			ArgDoc("buildArgs", "Additional build arguments.").
			ArgDoc("target", "Target build stage to build.").
			ArgDoc("secrets",
				`Secrets to pass to the build.`,
				`They will be mounted at /run/secrets/[secret-name] in the build container`,
				`They can be accessed in the Dockerfile using the "secret" mount type
				and mount path /run/secrets/[secret-name], e.g. RUN
				--mount=type=secret,id=my-secret curl [http://example.com?token=$(cat
				/run/secrets/my-secret)](http://example.com?token=$(cat
					/run/secrets/my-secret))`),

		dagql.Func("rootfs", s.rootfs).
			Doc(`Retrieves this container's root filesystem. Mounts are not included.`),

		dagql.Func("withRootfs", s.withRootfs).
			Doc(`Retrieves the container with the given directory mounted to /.`).
			ArgDoc("directory", "Directory to mount."),

		dagql.Func("directory", s.directory).
			Doc(`Retrieves a directory at the given path.`,
				`Mounts are included.`).
			ArgDoc("path", `The path of the directory to retrieve (e.g., "./src").`),

		dagql.Func("file", s.file).
			Doc(`Retrieves a file at the given path.`, `Mounts are included.`).
			ArgDoc("path", `The path of the file to retrieve (e.g., "./README.md").`),

		dagql.Func("user", s.user).
			Doc("Retrieves the user to be set for all commands."),

		dagql.Func("withUser", s.withUser).
			Doc(`Retrieves this container with a different command user.`).
			ArgDoc("name", `The user to set (e.g., "root").`),

		dagql.Func("withoutUser", s.withoutUser).
			Doc(`Retrieves this container with an unset command user.`,
				`Should default to root.`),

		dagql.Func("workdir", s.workdir).
			Doc("Retrieves the working directory for all commands."),

		dagql.Func("withWorkdir", s.withWorkdir).
			Doc(`Retrieves this container with a different working directory.`).
			ArgDoc("path", `The path to set as the working directory (e.g., "/app").`),

		dagql.Func("withoutWorkdir", s.withoutWorkdir).
			Doc(`Retrieves this container with an unset working directory.`,
				`Should default to "/".`),

		dagql.Func("envVariables", s.envVariables).
			Doc(`Retrieves the list of environment variables passed to commands.`),

		dagql.Func("envVariable", s.envVariable).
			Doc(`Retrieves the value of the specified environment variable.`).
			ArgDoc("name", `The name of the environment variable to retrieve (e.g., "PATH").`),

		dagql.Func("withEnvVariable", s.withEnvVariable).
			Doc(`Retrieves this container plus the given environment variable.`).
			ArgDoc("name", `The name of the environment variable (e.g., "HOST").`).
			ArgDoc("value", `The value of the environment variable. (e.g., "localhost").`).
			ArgDoc("expand",
				"Replace `${VAR}` or `$VAR` in the value according to the current "+
					`environment variables defined in the container (e.g.,
				"/opt/bin:$PATH").`),

		// NOTE: this is internal-only for now (hidden from codegen via the __ prefix) as we
		// currently only want to use it for allowing the Go SDK to inherit custom GOPROXY
		// settings from the engine container. It may be made public in the future with more
		// refined design.
		dagql.Func("__withSystemEnvVariable", s.withSystemEnvVariable).
			Doc(`(Internal-only) Inherit this environment variable from the engine container if set there with a special prefix.`),

		dagql.Func("withSecretVariable", s.withSecretVariable).
			Doc(`Retrieves this container plus an env variable containing the given secret.`).
			ArgDoc("name", `The name of the secret variable (e.g., "API_SECRET").`).
			ArgDoc("secret", `The identifier of the secret value.`),

		dagql.Func("withoutEnvVariable", s.withoutEnvVariable).
			Doc(`Retrieves this container minus the given environment variable.`).
			ArgDoc("name", `The name of the environment variable (e.g., "HOST").`),

		dagql.Func("withoutSecretVariable", s.withoutSecretVariable).
			Doc(`Retrieves this container minus the given environment variable containing the secret.`).
			ArgDoc("name", `The name of the environment variable (e.g., "HOST").`),

		dagql.Func("withLabel", s.withLabel).
			Doc(`Retrieves this container plus the given label.`).
			ArgDoc("name", `The name of the label (e.g., "org.opencontainers.artifact.created").`).
			ArgDoc("value", `The value of the label (e.g., "2023-01-01T00:00:00Z").`),

		dagql.Func("label", s.label).
			Doc(`Retrieves the value of the specified label.`).
			ArgDoc("name", `The name of the label (e.g., "org.opencontainers.artifact.created").`),

		dagql.Func("labels", s.labels).
			Doc(`Retrieves the list of labels passed to container.`),

		dagql.Func("withoutLabel", s.withoutLabel).
			Doc(`Retrieves this container minus the given environment label.`).
			ArgDoc("name", `The name of the label to remove (e.g., "org.opencontainers.artifact.created").`),

		dagql.Func("entrypoint", s.entrypoint).
			Doc(`Retrieves entrypoint to be prepended to the arguments of all commands.`),

		dagql.Func("withEntrypoint", s.withEntrypoint).
			Doc(`Retrieves this container but with a different command entrypoint.`).
			ArgDoc("args", `Entrypoint to use for future executions (e.g., ["go", "run"]).`).
			ArgDoc("keepDefaultArgs", `Don't remove the default arguments when setting the entrypoint.`),

		dagql.Func("withoutEntrypoint", s.withoutEntrypoint).
			Doc(`Retrieves this container with an unset command entrypoint.`).
			ArgDoc("keepDefaultArgs", `Don't remove the default arguments when unsetting the entrypoint.`),

		dagql.Func("defaultArgs", s.defaultArgs).
			Doc(`Retrieves default arguments for future commands.`),

		dagql.Func("withDefaultArgs", s.withDefaultArgs).
			Doc(`Configures default arguments for future commands.`).
			ArgDoc("args", `Arguments to prepend to future executions (e.g., ["-v", "--no-cache"]).`),

		dagql.Func("withoutDefaultArgs", s.withoutDefaultArgs).
			Doc(`Retrieves this container with unset default arguments for future commands.`),

		dagql.Func("mounts", s.mounts).
			Doc(`Retrieves the list of paths where a directory is mounted.`),

		dagql.Func("withMountedDirectory", s.withMountedDirectory).
			Doc(`Retrieves this container plus a directory mounted at the given path.`).
			ArgDoc("path", `Location of the mounted directory (e.g., "/mnt/directory").`).
			ArgDoc("source", `Identifier of the mounted directory.`).
			ArgDoc("owner",
				`A user:group to set for the mounted directory and its contents.`,
				`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
				`If the group is omitted, it defaults to the same as the user.`),

		dagql.Func("withMountedFile", s.withMountedFile).
			Doc(`Retrieves this container plus a file mounted at the given path.`).
			ArgDoc("path", `Location of the mounted file (e.g., "/tmp/file.txt").`).
			ArgDoc("source", `Identifier of the mounted file.`).
			ArgDoc("owner",
				`A user or user:group to set for the mounted file.`,
				`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
				`If the group is omitted, it defaults to the same as the user.`),

		dagql.Func("withMountedTemp", s.withMountedTemp).
			Doc(`Retrieves this container plus a temporary directory mounted at the given path. Any writes will be ephemeral to a single withExec call; they will not be persisted to subsequent withExecs.`).
			ArgDoc("path", `Location of the temporary directory (e.g., "/tmp/temp_dir").`),

		dagql.Func("withMountedCache", s.withMountedCache).
			Doc(`Retrieves this container plus a cache volume mounted at the given path.`).
			ArgDoc("path", `Location of the cache directory (e.g., "/cache/node_modules").`).
			ArgDoc("cache", `Identifier of the cache volume to mount.`).
			ArgDoc("source", `Identifier of the directory to use as the cache volume's root.`).
			ArgDoc("sharing", `Sharing mode of the cache volume.`).
			ArgDoc("owner",
				`A user:group to set for the mounted cache directory.`,
				`Note that this changes the ownership of the specified mount along with
				the initial filesystem provided by source (if any). It does not have
				any effect if/when the cache has already been created.`,
				`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
				`If the group is omitted, it defaults to the same as the user.`),

		dagql.Func("withMountedSecret", s.withMountedSecret).
			Doc(`Retrieves this container plus a secret mounted into a file at the given path.`).
			ArgDoc("path", `Location of the secret file (e.g., "/tmp/secret.txt").`).
			ArgDoc("source", `Identifier of the secret to mount.`).
			ArgDoc("owner",
				`A user:group to set for the mounted secret.`,
				`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
				`If the group is omitted, it defaults to the same as the user.`).
			ArgDoc("mode", `Permission given to the mounted secret (e.g., 0600).`,
				`This option requires an owner to be set to be active.`),

		dagql.Func("withUnixSocket", s.withUnixSocket).
			Doc(`Retrieves this container plus a socket forwarded to the given Unix socket path.`).
			ArgDoc("path", `Location of the forwarded Unix socket (e.g., "/tmp/socket").`).
			ArgDoc("source", `Identifier of the socket to forward.`).
			ArgDoc("owner",
				`A user:group to set for the mounted socket.`,
				`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
				`If the group is omitted, it defaults to the same as the user.`),

		dagql.Func("withoutUnixSocket", s.withoutUnixSocket).
			Doc(`Retrieves this container with a previously added Unix socket removed.`).
			ArgDoc("path", `Location of the socket to remove (e.g., "/tmp/socket").`),

		dagql.Func("withoutMount", s.withoutMount).
			Doc(`Retrieves this container after unmounting everything at the given path.`).
			ArgDoc("path", `Location of the cache directory (e.g., "/cache/node_modules").`),

		dagql.Func("withFile", s.withFile).
			Doc(`Retrieves this container plus the contents of the given file copied to the given path.`).
			ArgDoc("path", `Location of the copied file (e.g., "/tmp/file.txt").`).
			ArgDoc("source", `Identifier of the file to copy.`).
			ArgDoc("permissions", `Permission given to the copied file (e.g., 0600).`).
			ArgDoc("owner",
				`A user:group to set for the file.`,
				`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
				`If the group is omitted, it defaults to the same as the user.`),

		dagql.Func("withoutFile", s.withoutFile).
			Doc(`Retrieves this container with the file at the given path removed.`).
			ArgDoc("path", `Location of the file to remove (e.g., "/file.txt").`),

		dagql.Func("withFiles", s.withFiles).
			Doc(`Retrieves this container plus the contents of the given files copied to the given path.`).
			ArgDoc("path", `Location where copied files should be placed (e.g., "/src").`).
			ArgDoc("sources", `Identifiers of the files to copy.`).
			ArgDoc("permissions", `Permission given to the copied files (e.g., 0600).`).
			ArgDoc("owner",
				`A user:group to set for the files.`,
				`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
				`If the group is omitted, it defaults to the same as the user.`),

		dagql.Func("withNewFile", s.withNewFile).
			Doc(`Retrieves this container plus a new file written at the given path.`).
			ArgDoc("path", `Location of the written file (e.g., "/tmp/file.txt").`).
			ArgDoc("contents", `Content of the file to write (e.g., "Hello world!").`).
			ArgDoc("permissions", `Permission given to the written file (e.g., 0600).`).
			ArgDoc("owner",
				`A user:group to set for the file.`,
				`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
				`If the group is omitted, it defaults to the same as the user.`),

		dagql.Func("withDirectory", s.withDirectory).
			Doc(`Retrieves this container plus a directory written at the given path.`).
			ArgDoc("path", `Location of the written directory (e.g., "/tmp/directory").`).
			ArgDoc("directory", `Identifier of the directory to write`).
			ArgDoc("exclude", `Patterns to exclude in the written directory (e.g. ["node_modules/**", ".gitignore", ".git/"]).`).
			ArgDoc("include", `Patterns to include in the written directory (e.g. ["*.go", "go.mod", "go.sum"]).`).
			ArgDoc("owner",
				`A user:group to set for the directory and its contents.`,
				`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
				`If the group is omitted, it defaults to the same as the user.`),

		dagql.Func("withoutDirectory", s.withoutDirectory).
			Doc(`Retrieves this container with the directory at the given path removed.`).
			ArgDoc("path", `Location of the directory to remove (e.g., ".github/").`),

		dagql.Func("withExec", s.withExec).
			Doc(`Retrieves this container after executing the specified command inside it.`).
			ArgDoc("args",
				`Command to run instead of the container's default command (e.g., ["run", "main.go"]).`,
				`If empty, the container's default command is used.`).
			ArgDoc("skipEntrypoint",
				`If the container has an entrypoint, ignore it for args rather than using it to wrap them.`).
			ArgDoc("stdin",
				`Content to write to the command's standard input before closing (e.g.,
				"Hello world").`).
			ArgDoc("redirectStdout",
				`Redirect the command's standard output to a file in the container (e.g.,
			"/tmp/stdout").`).
			ArgDoc("redirectStderr",
				`Redirect the command's standard error to a file in the container (e.g.,
			"/tmp/stderr").`).
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

		dagql.Func("stdout", s.stdout).
			Doc(`The output stream of the last executed command.`,
				`Will execute default command if none is set, or error if there's no default.`),

		dagql.Func("stderr", s.stderr).
			Doc(`The error stream of the last executed command.`,
				`Will execute default command if none is set, or error if there's no default.`),

		dagql.Func("publish", s.publish).
			Impure("Writes to the specified Docker registry.").
			Doc(`Publishes this container as a new image to the specified address.`,
				`Publish returns a fully qualified ref.`,
				`It can also publish platform variants.`).
			ArgDoc("address",
				`Registry's address to publish the image to.`,
				`Formatted as [host]/[user]/[repo]:[tag] (e.g. "docker.io/dagger/dagger:main").`).
			ArgDoc("platformVariants",
				`Identifiers for other platform specific containers.`,
				`Used for multi-platform image.`).
			ArgDoc("forcedCompression",
				`Force each layer of the published image to use the specified
				compression algorithm.`,
				`If this is unset, then if a layer already has a compressed blob in the
				engine's cache, that will be used (this can result in a mix of
				compression algorithms for different layers). If this is unset and a
				layer has no compressed blob in the engine's cache, then it will be
				compressed using Gzip.`).
			ArgDoc("mediaTypes",
				`Use the specified media types for the published image's layers.`,
				`Defaults to OCI, which is largely compatible with most recent
				registries, but Docker may be needed for older registries without OCI
				support.`),

		dagql.Func("platform", s.platform).
			Doc(`The platform this container executes and publishes as.`),

		dagql.Func("export", s.export).
			Impure("Writes to the local host.").
			Doc(`Writes the container as an OCI tarball to the destination file path on the host.`,
				`Return true on success.`,
				`It can also export platform variants.`).
			ArgDoc("path",
				`Host's destination path (e.g., "./tarball").`,
				`Path can be relative to the engine's workdir or absolute.`).
			ArgDoc("platformVariants",
				`Identifiers for other platform specific containers.`,
				`Used for multi-platform image.`).
			ArgDoc("forcedCompression",
				`Force each layer of the exported image to use the specified compression algorithm.`,
				`If this is unset, then if a layer already has a compressed blob in the
				engine's cache, that will be used (this can result in a mix of
				compression algorithms for different layers). If this is unset and a
				layer has no compressed blob in the engine's cache, then it will be
				compressed using Gzip.`).
			ArgDoc("mediaTypes",
				`Use the specified media types for the exported image's layers.`,
				`Defaults to OCI, which is largely compatible with most recent
				container runtimes, but Docker may be needed for older runtimes without
				OCI support.`),

		dagql.Func("asTarball", s.asTarball).
			Doc(`Returns a File representing the container serialized to a tarball.`).
			ArgDoc("platformVariants",
				`Identifiers for other platform specific containers.`,
				`Used for multi-platform images.`).
			ArgDoc("forcedCompression",
				`Force each layer of the image to use the specified compression algorithm.`,
				`If this is unset, then if a layer already has a compressed blob in the
				engine's cache, that will be used (this can result in a mix of
				compression algorithms for different layers). If this is unset and a
				layer has no compressed blob in the engine's cache, then it will be
				compressed using Gzip.`).
			ArgDoc("mediaTypes", `Use the specified media types for the image's layers.`,
				`Defaults to OCI, which is largely compatible with most recent
				container runtimes, but Docker may be needed for older runtimes without
				OCI support.`),

		dagql.Func("import", s.import_).
			Doc(`Reads the container from an OCI tarball.`).
			ArgDoc("source", `File to read the container from.`).
			ArgDoc("tag", `Identifies the tag to import from the archive, if the archive bundles multiple tags.`),

		dagql.Func("withRegistryAuth", s.withRegistryAuth).
			Doc(`Retrieves this container with a registry authentication for a given address.`).
			ArgDoc("address",
				`Registry's address to bind the authentication to.`,
				`Formatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main).`).
			ArgDoc("username", `The username of the registry's account (e.g., "Dagger").`).
			ArgDoc("secret", `The API key, password or token to authenticate to this registry.`),

		dagql.Func("withoutRegistryAuth", s.withoutRegistryAuth).
			Doc(`Retrieves this container without the registry authentication of a given address.`).
			ArgDoc("address", `Registry's address to remove the authentication from.`,
				`Formatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main).`),

		dagql.Func("imageRef", s.imageRef).
			Doc(`The unique image reference which can only be retrieved immediately after the 'Container.From' call.`),

		dagql.Func("withExposedPort", s.withExposedPort).
			Doc(`Expose a network port.`,
				`Exposed ports serve two purposes:`,
				`- For health checks and introspection, when running services`,
				`- For setting the EXPOSE OCI field when publishing the container`).
			ArgDoc("port", `Port number to expose`).
			ArgDoc("protocol", `Transport layer network protocol`).
			ArgDoc("description", `Optional port description`).
			ArgDoc("experimentalSkipHealthcheck", `Skip the health check when run as a service.`),

		dagql.Func("withoutExposedPort", s.withoutExposedPort).
			Doc(`Unexpose a previously exposed port.`).
			ArgDoc("port", `Port number to unexpose`).
			ArgDoc("protocol", `Port protocol to unexpose`),

		dagql.Func("exposedPorts", s.exposedPorts).
			Doc(`Retrieves the list of exposed ports.`,
				`This includes ports already exposed by the image, even if not explicitly added with dagger.`),

		dagql.Func("withServiceBinding", s.withServiceBinding).
			Doc(`Establish a runtime dependency on a service.`,
				`The service will be started automatically when needed and detached
				when it is no longer needed, executing the default command if none is
				set.`,
				`The service will be reachable from the container via the provided hostname alias.`,
				`The service dependency will also convey to any files or directories produced by the container.`).
			ArgDoc("alias", `A name that can be used to reach the service from the container`).
			ArgDoc("service", `Identifier of the service container`),

		dagql.Func("withFocus", s.withFocus).
			Doc(`Indicate that subsequent operations should be featured more prominently in the UI.`),

		dagql.Func("withoutFocus", s.withoutFocus).
			Doc(`Indicate that subsequent operations should not be featured more prominently in the UI.`,
				`This is the initial state of all containers.`),

		dagql.Func("withDefaultTerminalCmd", s.withDefaultTerminalCmd).
			Doc(`Set the default command to invoke for the container's terminal API.`).
			ArgDoc("args", `The args of the command.`).
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

		dagql.NodeFunc("terminal", s.terminal).
			Impure("Nondeterministic.").
			Doc(`Opens an interactive terminal for this container using its configured default terminal command if not overridden by args (or sh as a fallback default).`).
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

		dagql.Func("experimentalWithGPU", s.withGPU).
			Doc(`EXPERIMENTAL API! Subject to change/removal at any time.`,
				`Configures the provided list of devices to be accessible to this container.`,
				`This currently works for Nvidia devices only.`).
			ArgDoc("devices", `List of devices to be accessible to this container.`),

		dagql.Func("experimentalWithAllGPUs", s.withAllGPUs).
			Doc(`EXPERIMENTAL API! Subject to change/removal at any time.`,
				`Configures all available GPUs on the host to be accessible to this container.`,
				`This currently works for Nvidia devices only.`),
	}.Install(s.srv)
}

type containerArgs struct {
	ID       dagql.Optional[core.ContainerID]
	Platform dagql.Optional[core.Platform]
}

func (s *containerSchema) container(ctx context.Context, parent *core.Query, args containerArgs) (_ *core.Container, rerr error) {
	if args.ID.Valid {
		inst, err := args.ID.Value.Load(ctx, s.srv)
		if err != nil {
			return nil, err
		}
		// NB: what we kind of want is to return an Instance[*core.Container] in
		// this case, but this API is deprecated anyhow
		return inst.Self, nil
	}
	var platform core.Platform
	if args.Platform.Valid {
		platform = args.Platform.Value
	} else {
		platform = parent.Platform
	}
	return parent.NewContainer(platform), nil
}

type containerFromArgs struct {
	Address string
}

func (s *containerSchema) from(ctx context.Context, parent *core.Container, args containerFromArgs) (*core.Container, error) {
	return parent.From(ctx, args.Address)
}

type containerBuildArgs struct {
	Context    core.DirectoryID
	Dockerfile string                             `default:"Dockerfile"`
	Target     string                             `default:""`
	BuildArgs  []dagql.InputObject[core.BuildArg] `default:"[]"`
	Secrets    []core.SecretID                    `default:"[]"`
}

func (s *containerSchema) build(ctx context.Context, parent *core.Container, args containerBuildArgs) (*core.Container, error) {
	dir, err := args.Context.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	secrets, err := dagql.LoadIDs(ctx, s.srv, args.Secrets)
	if err != nil {
		return nil, err
	}
	return parent.Build(
		ctx,
		dir.Self,
		args.Dockerfile,
		collectInputsSlice(args.BuildArgs),
		args.Target,
		secrets,
	)
}

type containerWithRootFSArgs struct {
	Directory core.DirectoryID
}

func (s *containerSchema) withRootfs(ctx context.Context, parent *core.Container, args containerWithRootFSArgs) (*core.Container, error) {
	dir, err := args.Directory.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return parent.WithRootFS(ctx, dir.Self)
}

type containerPipelineArgs struct {
	Name        string
	Description string                             `default:""`
	Labels      []dagql.InputObject[PipelineLabel] `default:"[]"`
}

func (s *containerSchema) pipeline(ctx context.Context, parent *core.Container, args containerPipelineArgs) (*core.Container, error) {
	return parent.WithPipeline(ctx, args.Name, args.Description)
}

func (s *containerSchema) rootfs(ctx context.Context, parent *core.Container, args struct{}) (*core.Directory, error) {
	return parent.RootFS(ctx)
}

type containerExecArgs struct {
	core.ContainerExecOpts
}

func (s *containerSchema) withExec(ctx context.Context, parent *core.Container, args containerExecArgs) (*core.Container, error) {
	return parent.WithExec(ctx, args.ContainerExecOpts)
}

func (s *containerSchema) stdout(ctx context.Context, parent *core.Container, _ struct{}) (string, error) {
	return parent.MetaFileContents(ctx, buildkit.MetaMountStdoutPath)
}

func (s *containerSchema) stderr(ctx context.Context, parent *core.Container, _ struct{}) (string, error) {
	return parent.MetaFileContents(ctx, buildkit.MetaMountStderrPath)
}

type containerGpuArgs struct {
	core.ContainerGPUOpts
}

func (s *containerSchema) withGPU(ctx context.Context, parent *core.Container, args containerGpuArgs) (*core.Container, error) {
	return parent.WithGPU(ctx, args.ContainerGPUOpts)
}

func (s *containerSchema) withAllGPUs(ctx context.Context, parent *core.Container, args struct{}) (*core.Container, error) {
	return parent.WithGPU(ctx, core.ContainerGPUOpts{Devices: []string{"all"}})
}

type containerWithEntrypointArgs struct {
	Args            []string
	KeepDefaultArgs bool `default:"false"`
}

func (s *containerSchema) withEntrypoint(ctx context.Context, parent *core.Container, args containerWithEntrypointArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.Entrypoint = args.Args
		if !args.KeepDefaultArgs {
			cfg.Cmd = nil
		}
		return cfg
	})
}

type containerWithoutEntrypointArgs struct {
	KeepDefaultArgs bool `default:"false"`
}

func (s *containerSchema) withoutEntrypoint(ctx context.Context, parent *core.Container, args containerWithoutEntrypointArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.Entrypoint = nil
		if !args.KeepDefaultArgs {
			cfg.Cmd = nil
		}
		return cfg
	})
}

func (s *containerSchema) entrypoint(ctx context.Context, parent *core.Container, args struct{}) ([]string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	return cfg.Entrypoint, nil
}

type containerWithDefaultArgs struct {
	Args []string
}

func (s *containerSchema) withDefaultArgs(ctx context.Context, parent *core.Container, args containerWithDefaultArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		if args.Args == nil {
			cfg.Cmd = []string{}
			return cfg
		}

		cfg.Cmd = args.Args
		return cfg
	})
}

func (s *containerSchema) withoutDefaultArgs(ctx context.Context, parent *core.Container, _ struct{}) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.Cmd = nil
		return cfg
	})
}

func (s *containerSchema) defaultArgs(ctx context.Context, parent *core.Container, args struct{}) ([]string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	return cfg.Cmd, nil
}

type containerWithUserArgs struct {
	Name string
}

func (s *containerSchema) withUser(ctx context.Context, parent *core.Container, args containerWithUserArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.User = args.Name
		return cfg
	})
}

func (s *containerSchema) withoutUser(ctx context.Context, parent *core.Container, _ struct{}) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.User = ""
		return cfg
	})
}

func (s *containerSchema) user(ctx context.Context, parent *core.Container, args struct{}) (string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return "", err
	}

	return cfg.User, nil
}

type containerWithWorkdirArgs struct {
	Path string
}

func (s *containerSchema) withWorkdir(ctx context.Context, parent *core.Container, args containerWithWorkdirArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.WorkingDir = absPath(cfg.WorkingDir, args.Path)
		return cfg
	})
}

func (s *containerSchema) withoutWorkdir(ctx context.Context, parent *core.Container, _ struct{}) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.WorkingDir = ""
		return cfg
	})
}

func (s *containerSchema) workdir(ctx context.Context, parent *core.Container, args struct{}) (string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return "", err
	}

	return cfg.WorkingDir, nil
}

type containerWithVariableArgs struct {
	Name   string
	Value  string
	Expand bool `default:"false"`
}

func (s *containerSchema) withEnvVariable(ctx context.Context, parent *core.Container, args containerWithVariableArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		value := args.Value

		if args.Expand {
			value = os.Expand(value, func(k string) string {
				v, _ := core.LookupEnv(cfg.Env, k)
				return v
			})
		}

		cfg.Env = core.AddEnv(cfg.Env, args.Name, value)

		return cfg
	})
}

type containerWithSystemEnvArgs struct {
	Name string
}

func (s *containerSchema) withSystemEnvVariable(ctx context.Context, parent *core.Container, args containerWithSystemEnvArgs) (*core.Container, error) {
	ctr := parent.Clone()
	ctr.SystemEnvNames = append(ctr.SystemEnvNames, args.Name)
	return ctr, nil
}

type containerWithoutVariableArgs struct {
	Name string
}

func (s *containerSchema) withoutEnvVariable(ctx context.Context, parent *core.Container, args containerWithoutVariableArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		newEnv := []string{}

		core.WalkEnv(cfg.Env, func(k, _, env string) {
			if !shell.EqualEnvKeys(k, args.Name) {
				newEnv = append(newEnv, env)
			}
		})

		cfg.Env = newEnv

		return cfg
	})
}

type EnvVariable struct {
	Name  string `field:"true" doc:"The environment variable name."`
	Value string `field:"true" doc:"The environment variable value."`
}

func (EnvVariable) Type() *ast.Type {
	return &ast.Type{
		NamedType: "EnvVariable",
		NonNull:   true,
	}
}

func (EnvVariable) TypeDescription() string {
	return "An environment variable name and value."
}

func (EnvVariable) Description() string {
	return "A simple key value object that represents an environment variable."
}

func (s *containerSchema) envVariables(ctx context.Context, parent *core.Container, args struct{}) ([]EnvVariable, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	vars := make([]EnvVariable, 0, len(cfg.Env))

	core.WalkEnv(cfg.Env, func(k, v, _ string) {
		vars = append(vars, EnvVariable{Name: k, Value: v})
	})

	return vars, nil
}

type containerVariableArgs struct {
	Name string
}

func (s *containerSchema) envVariable(ctx context.Context, parent *core.Container, args containerVariableArgs) (dagql.Nullable[dagql.String], error) {
	none := dagql.Null[dagql.String]()

	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return none, err
	}

	if val, ok := core.LookupEnv(cfg.Env, args.Name); ok {
		return dagql.NonNull(dagql.NewString(val)), nil
	}

	return none, nil
}

type Label struct {
	Name  string `field:"true" doc:"The label name."`
	Value string `field:"true" doc:"The label value."`
}

func (Label) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Label",
		NonNull:   true,
	}
}

func (Label) TypeDescription() string {
	return "A simple key value object that represents a label."
}

func (s *containerSchema) labels(ctx context.Context, parent *core.Container, args struct{}) ([]Label, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	labels := make([]Label, 0, len(cfg.Labels))
	for name, value := range cfg.Labels {
		label := Label{
			Name:  name,
			Value: value,
		}

		labels = append(labels, label)
	}

	// FIXME(vito): sort, test; order must be stable for IDs to work as expected

	return labels, nil
}

type containerLabelArgs struct {
	Name string
}

func (s *containerSchema) label(ctx context.Context, parent *core.Container, args containerLabelArgs) (dagql.Nullable[dagql.String], error) {
	none := dagql.Null[dagql.String]()

	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return none, err
	}

	if val, ok := cfg.Labels[args.Name]; ok {
		return dagql.NonNull(dagql.NewString(val)), nil
	}

	return none, nil
}

type containerWithMountedDirectoryArgs struct {
	Path   string
	Source core.DirectoryID
	Owner  string `default:""`
}

func (s *containerSchema) withMountedDirectory(ctx context.Context, parent *core.Container, args containerWithMountedDirectoryArgs) (*core.Container, error) {
	dir, err := args.Source.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return parent.WithMountedDirectory(ctx, args.Path, dir.Self, args.Owner, false)
}

type containerPublishArgs struct {
	Address           dagql.String
	PlatformVariants  []core.ContainerID `default:"[]"`
	ForcedCompression dagql.Optional[core.ImageLayerCompression]
	MediaTypes        core.ImageMediaTypes `default:"OCIMediaTypes"`
}

func (s *containerSchema) publish(ctx context.Context, parent *core.Container, args containerPublishArgs) (dagql.String, error) {
	variants, err := dagql.LoadIDs(ctx, s.srv, args.PlatformVariants)
	if err != nil {
		return "", err
	}
	ref, err := parent.Publish(
		ctx,
		args.Address.String(),
		variants,
		args.ForcedCompression.Value,
		args.MediaTypes,
	)
	if err != nil {
		return "", err
	}
	return dagql.NewString(ref), nil
}

type containerWithMountedFileArgs struct {
	Path   string
	Source core.FileID
	Owner  string `default:""`
}

func (s *containerSchema) withMountedFile(ctx context.Context, parent *core.Container, args containerWithMountedFileArgs) (*core.Container, error) {
	file, err := args.Source.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return parent.WithMountedFile(ctx, args.Path, file.Self, args.Owner, false)
}

type containerWithMountedCacheArgs struct {
	Path    string
	Cache   core.CacheVolumeID
	Source  dagql.Optional[core.DirectoryID]
	Sharing core.CacheSharingMode `default:"SHARED"`
	Owner   string                `default:""`
}

func (s *containerSchema) withMountedCache(ctx context.Context, parent *core.Container, args containerWithMountedCacheArgs) (*core.Container, error) {
	var dir *core.Directory
	if args.Source.Valid {
		inst, err := args.Source.Value.Load(ctx, s.srv)
		if err != nil {
			return nil, err
		}
		dir = inst.Self
	}

	cache, err := args.Cache.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}

	return parent.WithMountedCache(
		ctx,
		args.Path,
		cache.Self,
		dir,
		args.Sharing,
		args.Owner,
	)
}

type containerWithMountedTempArgs struct {
	Path string
}

func (s *containerSchema) withMountedTemp(ctx context.Context, parent *core.Container, args containerWithMountedTempArgs) (*core.Container, error) {
	return parent.WithMountedTemp(ctx, args.Path)
}

type containerWithoutMountArgs struct {
	Path string
}

func (s *containerSchema) withoutMount(ctx context.Context, parent *core.Container, args containerWithoutMountArgs) (*core.Container, error) {
	return parent.WithoutMount(ctx, args.Path)
}

func (s *containerSchema) mounts(ctx context.Context, parent *core.Container, _ struct{}) (dagql.Array[dagql.String], error) {
	targets, err := parent.MountTargets(ctx)
	if err != nil {
		return nil, err
	}
	return dagql.NewStringArray(targets...), nil
}

type containerWithLabelArgs struct {
	Name  string
	Value string
}

func (s *containerSchema) withLabel(ctx context.Context, parent *core.Container, args containerWithLabelArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		if cfg.Labels == nil {
			cfg.Labels = make(map[string]string)
		}
		cfg.Labels[args.Name] = args.Value
		return cfg
	})
}

type containerWithoutLabelArgs struct {
	Name string
}

func (s *containerSchema) withoutLabel(ctx context.Context, parent *core.Container, args containerWithoutLabelArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		delete(cfg.Labels, args.Name)
		return cfg
	})
}

type containerDirectoryArgs struct {
	Path string
}

func (s *containerSchema) directory(ctx context.Context, parent *core.Container, args containerDirectoryArgs) (*core.Directory, error) {
	return parent.Directory(ctx, args.Path)
}

type containerFileArgs struct {
	Path string
}

func (s *containerSchema) file(ctx context.Context, parent *core.Container, args containerFileArgs) (*core.File, error) {
	return parent.File(ctx, args.Path)
}

func absPath(workDir string, containerPath string) string {
	if path.IsAbs(containerPath) {
		return containerPath
	}

	if workDir == "" {
		workDir = "/"
	}

	return path.Join(workDir, containerPath)
}

type containerWithSecretVariableArgs struct {
	Name   string
	Secret core.SecretID
}

func (s *containerSchema) withSecretVariable(ctx context.Context, parent *core.Container, args containerWithSecretVariableArgs) (*core.Container, error) {
	secret, err := args.Secret.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return parent.WithSecretVariable(ctx, args.Name, secret.Self)
}

type containerWithoutSecretVariableArgs struct {
	Name string
}

func (s *containerSchema) withoutSecretVariable(ctx context.Context, parent *core.Container, args containerWithoutSecretVariableArgs) (*core.Container, error) {
	return parent.WithoutSecretVariable(ctx, args.Name)
}

type containerWithMountedSecretArgs struct {
	Path   string
	Source core.SecretID
	Owner  string `default:""`
	Mode   int    `default:"0400"` // FIXME(vito): verify octal
}

func (s *containerSchema) withMountedSecret(ctx context.Context, parent *core.Container, args containerWithMountedSecretArgs) (*core.Container, error) {
	secret, err := args.Source.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return parent.WithMountedSecret(ctx, args.Path, secret.Self, args.Owner, fs.FileMode(args.Mode))
}

type containerWithDirectoryArgs struct {
	WithDirectoryArgs
	Owner string `default:""`
}

func (s *containerSchema) withDirectory(ctx context.Context, parent *core.Container, args containerWithDirectoryArgs) (*core.Container, error) {
	dir, err := args.Directory.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return parent.WithDirectory(ctx, args.Path, dir.Self, args.CopyFilter, args.Owner)
}

type containerWithFileArgs struct {
	WithFileArgs
	Owner string `default:""`
}

func (s *containerSchema) withFile(ctx context.Context, parent *core.Container, args containerWithFileArgs) (*core.Container, error) {
	file, err := args.Source.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return parent.WithFile(ctx, args.Path, file.Self, args.Permissions, args.Owner)
}

type containerWithFilesArgs struct {
	WithFilesArgs
	Owner string `default:""`
}

func (s *containerSchema) withFiles(ctx context.Context, parent *core.Container, args containerWithFilesArgs) (*core.Container, error) {
	files := []*core.File{}
	for _, id := range args.Sources {
		file, err := id.Load(ctx, s.srv)
		if err != nil {
			return nil, err
		}
		files = append(files, file.Self)
	}

	return parent.WithFiles(ctx, args.Path, files, args.Permissions, args.Owner)
}

type containerWithoutDirectoryArgs struct {
	Path string
}

func (s *containerSchema) withoutDirectory(ctx context.Context, parent *core.Container, args containerWithoutDirectoryArgs) (*core.Container, error) {
	return parent.WithoutPath(ctx, args.Path)
}

type containerWithoutFileArgs struct {
	Path string
}

func (s *containerSchema) withoutFile(ctx context.Context, parent *core.Container, args containerWithoutFileArgs) (*core.Container, error) {
	return parent.WithoutPath(ctx, args.Path)
}

type containerWithNewFileArgs struct {
	Path        string
	Contents    string `default:""`
	Permissions int    `default:"0644"`
	Owner       string `default:""`
}

func (s *containerSchema) withNewFile(ctx context.Context, parent *core.Container, args containerWithNewFileArgs) (*core.Container, error) {
	return parent.WithNewFile(ctx, args.Path, []byte(args.Contents), fs.FileMode(args.Permissions), args.Owner)
}

type containerWithUnixSocketArgs struct {
	Path   string
	Source core.SocketID
	Owner  string `default:""`
}

func (s *containerSchema) withUnixSocket(ctx context.Context, parent *core.Container, args containerWithUnixSocketArgs) (*core.Container, error) {
	socket, err := args.Source.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return parent.WithUnixSocket(ctx, args.Path, socket.Self, args.Owner)
}

type containerWithoutUnixSocketArgs struct {
	Path string
}

func (s *containerSchema) withoutUnixSocket(ctx context.Context, parent *core.Container, args containerWithoutUnixSocketArgs) (*core.Container, error) {
	return parent.WithoutUnixSocket(ctx, args.Path)
}

func (s *containerSchema) platform(ctx context.Context, parent *core.Container, args struct{}) (core.Platform, error) {
	return parent.Platform, nil
}

type containerExportArgs struct {
	Path              string
	PlatformVariants  []core.ContainerID `default:"[]"`
	ForcedCompression dagql.Optional[core.ImageLayerCompression]
	MediaTypes        core.ImageMediaTypes `default:"OCIMediaTypes"`
}

func (s *containerSchema) export(ctx context.Context, parent *core.Container, args containerExportArgs) (dagql.Boolean, error) {
	variants, err := dagql.LoadIDs(ctx, s.srv, args.PlatformVariants)
	if err != nil {
		return false, err
	}
	if err := parent.Export(
		ctx,
		args.Path,
		variants,
		args.ForcedCompression.Value,
		args.MediaTypes,
	); err != nil {
		return false, err
	}

	return true, nil
}

type containerAsTarballArgs struct {
	PlatformVariants  []core.ContainerID `default:"[]"`
	ForcedCompression dagql.Optional[core.ImageLayerCompression]
	MediaTypes        core.ImageMediaTypes `default:"OCIMediaTypes"`
}

func (s *containerSchema) asTarball(ctx context.Context, parent *core.Container, args containerAsTarballArgs) (*core.File, error) {
	variants, err := dagql.LoadIDs(ctx, s.srv, args.PlatformVariants)
	if err != nil {
		return nil, err
	}
	return parent.AsTarball(ctx, variants, args.ForcedCompression.Value, args.MediaTypes)
}

type containerImportArgs struct {
	Source core.FileID
	Tag    string `default:""`
}

func (s *containerSchema) import_(ctx context.Context, parent *core.Container, args containerImportArgs) (*core.Container, error) {
	start := time.Now()
	slog.ExtraDebug("importing container", "source", args.Source.Display(), "tag", args.Tag)
	defer func() {
		slog.ExtraDebug("done importing container", "source", args.Source.Display(), "tag", args.Tag, "took", start)
	}()
	source, err := args.Source.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return parent.Import(
		ctx,
		source.Self,
		args.Tag,
	)
}

type containerWithRegistryAuthArgs struct {
	Address  string
	Username string
	Secret   core.SecretID
}

func (s *containerSchema) withRegistryAuth(ctx context.Context, parent *core.Container, args containerWithRegistryAuthArgs) (*core.Container, error) {
	secret, err := args.Secret.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}

	secretBytes, err := parent.Query.Secrets.GetSecret(ctx, secret.Self.Accessor)
	if err != nil {
		return nil, err
	}

	if err := parent.Query.Auth.AddCredential(args.Address, args.Username, string(secretBytes)); err != nil {
		return nil, err
	}

	return parent, nil
}

type containerWithoutRegistryAuthArgs struct {
	Address string
}

func (s *containerSchema) withoutRegistryAuth(_ context.Context, parent *core.Container, args containerWithoutRegistryAuthArgs) (*core.Container, error) {
	if err := parent.Query.Auth.RemoveCredential(args.Address); err != nil {
		return nil, err
	}

	return parent, nil
}

func (s *containerSchema) imageRef(ctx context.Context, parent *core.Container, args struct{}) (string, error) {
	return parent.ImageRefOrErr(ctx)
}

type containerWithServiceBindingArgs struct {
	Alias   string
	Service core.ServiceID
}

func (s *containerSchema) withServiceBinding(ctx context.Context, parent *core.Container, args containerWithServiceBindingArgs) (*core.Container, error) {
	svc, err := args.Service.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}

	return parent.WithServiceBinding(ctx, svc.ID(), svc.Self, args.Alias)
}

type containerWithExposedPortArgs struct {
	Port                        int
	Protocol                    core.NetworkProtocol `default:"TCP"`
	Description                 *string
	ExperimentalSkipHealthcheck bool `default:"false"`
}

func (s *containerSchema) withExposedPort(ctx context.Context, parent *core.Container, args containerWithExposedPortArgs) (*core.Container, error) {
	return parent.WithExposedPort(core.Port{
		Protocol:                    args.Protocol,
		Port:                        args.Port,
		Description:                 args.Description,
		ExperimentalSkipHealthcheck: args.ExperimentalSkipHealthcheck,
	})
}

type containerWithoutExposedPortArgs struct {
	Port     int
	Protocol core.NetworkProtocol `default:"TCP"`
}

func (s *containerSchema) withoutExposedPort(ctx context.Context, parent *core.Container, args containerWithoutExposedPortArgs) (*core.Container, error) {
	return parent.WithoutExposedPort(args.Port, args.Protocol)
}

func (s *containerSchema) exposedPorts(ctx context.Context, parent *core.Container, args struct{}) ([]core.Port, error) {
	// get descriptions from `Container.Ports` (not in the OCI spec)
	ports := make(map[string]core.Port, len(parent.Ports))
	for _, p := range parent.Ports {
		ociPort := fmt.Sprintf("%d/%s", p.Port, p.Protocol.Network())
		ports[ociPort] = p
	}

	exposedPorts := []core.Port{}
	for ociPort := range parent.Config.ExposedPorts {
		p, exists := ports[ociPort]
		if !exists {
			// ignore errors when parsing from OCI
			port, protoStr, ok := strings.Cut(ociPort, "/")
			if !ok {
				continue
			}
			portNr, err := strconv.Atoi(port)
			if err != nil {
				continue
			}
			proto, err := core.NetworkProtocols.Lookup(strings.ToUpper(protoStr))
			if err != nil {
				// FIXME(vito): should this and above return nil, err instead?
				continue
			}
			p = core.Port{
				Port:     portNr,
				Protocol: proto,
			}
		}
		exposedPorts = append(exposedPorts, p)
	}

	return exposedPorts, nil
}

func (s *containerSchema) withFocus(ctx context.Context, parent *core.Container, args struct{}) (*core.Container, error) {
	child := parent.Clone()
	child.Focused = true
	return child, nil
}

func (s *containerSchema) withoutFocus(ctx context.Context, parent *core.Container, args struct{}) (*core.Container, error) {
	child := parent.Clone()
	child.Focused = false
	return child, nil
}

type containerWithDefaultTerminalCmdArgs struct {
	core.DefaultTerminalCmdOpts
}

func (s *containerSchema) withDefaultTerminalCmd(
	ctx context.Context,
	ctr *core.Container,
	args containerWithDefaultTerminalCmdArgs,
) (*core.Container, error) {
	ctr = ctr.Clone()
	ctr.DefaultTerminalCmd = args.DefaultTerminalCmdOpts
	return ctr, nil
}

type containerTerminalArgs struct {
	core.TerminalArgs
}

func (s *containerSchema) terminal(
	ctx context.Context,
	ctr dagql.Instance[*core.Container],
	args containerTerminalArgs,
) (dagql.Instance[*core.Container], error) {
	if args.Cmd == nil || len(args.Cmd) == 0 {
		args.Cmd = ctr.Self.DefaultTerminalCmd.Args
	}

	if args.ExperimentalPrivilegedNesting == nil {
		args.ExperimentalPrivilegedNesting = &ctr.Self.DefaultTerminalCmd.ExperimentalPrivilegedNesting
	}

	if args.InsecureRootCapabilities == nil {
		args.InsecureRootCapabilities = &ctr.Self.DefaultTerminalCmd.InsecureRootCapabilities
	}

	// if still no args, default to sh
	if len(args.Cmd) == 0 {
		args.Cmd = []string{"sh"}
	}

	err := ctr.Self.Terminal(ctx, ctr.ID(), &args.TerminalArgs)
	if err != nil {
		return ctr, err
	}

	return ctr, nil
}
