package schema

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/internal/buildkit/frontend/dockerfile/shell"
	"github.com/dagger/dagger/util/hashutil"
	telemetry "github.com/dagger/otel-go"
	"github.com/distribution/reference"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	"github.com/dagger/dagger/engine/slog"
)

type containerSchema struct{}

var _ SchemaResolvers = &containerSchema{}

func (s *containerSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("container", s.container).
			Doc(`Creates a scratch container, with no image or metadata.`,
				`To pull an image, follow up with the "from" function.`).
			Args(
				dagql.Arg("platform").Doc(`Platform to initialize the container with. Defaults to the native platform of the current engine`),
			),
	}.Install(srv)

	dagql.Fields[*core.Container]{
		Syncer[*core.Container]().
			Doc(`Forces evaluation of the pipeline in the engine.`,
				`It doesn't run the default command if no exec has been set.`),

		dagql.Func("pipeline", s.pipeline).
			View(BeforeVersion("v0.13.0")).
			Deprecated("Explicit pipeline creation is now a no-op").
			Doc(`Creates a named sub-pipeline.`).
			Args(
				dagql.Arg("name").Doc("Name of the sub-pipeline."),
				dagql.Arg("description").Doc("Description of the sub-pipeline."),
				dagql.Arg("labels").Doc("Labels to apply to the sub-pipeline."),
			),

		dagql.NodeFunc("from", s.from).
			IsPersistable().
			WithInput(fromSessionScopeInput).
			Doc(`Download a container image, and apply it to the container state. All previous state will be lost.`).
			Args(
				dagql.Arg("address").Doc(
					`Address of the container image to download, in standard OCI ref format. Example:"registry.dagger.io/engine:latest"`,
				),
			),
		dagql.NodeFunc("build", s.build).
			View(BeforeVersion("v0.19.0")).
			Deprecated("Use `Directory.build` instead").
			Doc(`Initializes this container from a Dockerfile build.`).
			Args(
				dagql.Arg("context").Doc("Directory context used by the Dockerfile."),
				dagql.Arg("dockerfile").Doc("Path to the Dockerfile to use."),
				dagql.Arg("target").Doc("Target build stage to build."),
				dagql.Arg("buildArgs").Doc("Additional build arguments."),
				dagql.Arg("secrets").Doc(`Secrets to pass to the build.`,
					`They will be mounted at /run/secrets/[secret-name] in the build container`,
					`They can be accessed in the Dockerfile using the "secret" mount type
					and mount path /run/secrets/[secret-name], e.g. RUN
					--mount=type=secret,id=my-secret curl [http://example.com?token=$(cat
					/run/secrets/my-secret)](http://example.com?token=$(cat
						/run/secrets/my-secret))`),
				dagql.Arg("noInit").Doc(`If set, skip the automatic init process injected into containers created by RUN statements.`,
					`This should only be used if the user requires that their exec processes be the
					pid 1 process in the container. Otherwise it may result in unexpected behavior.`,
				),
			),

		dagql.NodeFunc("rootfs", s.rootfs).
			Doc(`Return a snapshot of the container's root filesystem. The snapshot can be modified then written back using withRootfs. Use that method for filesystem modifications.`),
		dagql.NodeFunc("withRootfs", s.withRootfs).
			Doc(`Change the container's root filesystem. The previous root filesystem will be lost.`).
			Args(
				dagql.Arg("directory").Doc("The new root filesystem."),
			),
		dagql.NodeFunc("directory", s.directory).
			Doc(`Retrieve a directory from the container's root filesystem`,
				`Mounts are included.`).
			Args(
				dagql.Arg("path").Doc(`The path of the directory to retrieve (e.g., "./src").`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
					`environment variables defined in the container (e.g. "/$VAR/foo").`),
			),

		dagql.NodeFunc("file", s.file).
			Doc(`Retrieves a file at the given path.`, `Mounts are included.`).
			Args(
				dagql.Arg("path").Doc(`The path of the file to retrieve (e.g., "./README.md").`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
					`environment variables defined in the container (e.g. "/$VAR/foo.txt").`),
			),

		dagql.Func("user", s.user).
			Doc("Retrieves the user to be set for all commands."),

		dagql.NodeFunc("withUser", s.withUser).
			Doc(`Retrieves this container with a different command user.`).
			Args(
				dagql.Arg("name").Doc(`The user to set (e.g., "root").`),
			),

		dagql.NodeFunc("withoutUser", s.withoutUser).
			Doc(`Retrieves this container with an unset command user.`,
				`Should default to root.`),

		dagql.Func("workdir", s.workdir).
			Doc("Retrieves the working directory for all commands."),

		dagql.NodeFunc("withWorkdir", s.withWorkdir).
			Doc(`Change the container's working directory. Like WORKDIR in Dockerfile.`).
			Args(
				dagql.Arg("path").Doc(`The path to set as the working directory (e.g., "/app").`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
					`environment variables defined in the container (e.g. "/$VAR/foo").`),
			),

		dagql.NodeFunc("withoutWorkdir", s.withoutWorkdir).
			Doc(`Unset the container's working directory.`,
				`Should default to "/".`),

		dagql.Func("envVariables", s.envVariables).
			Doc(`Retrieves the list of environment variables passed to commands.`),

		dagql.Func("envVariable", s.envVariable).
			Doc(`Retrieves the value of the specified environment variable.`).
			Args(
				dagql.Arg("name").Doc(`The name of the environment variable to retrieve (e.g., "PATH").`),
			),

		dagql.NodeFunc("withEnvVariable", s.withEnvVariable).
			Doc(`Set a new environment variable in the container.`).
			Args(
				dagql.Arg("name").Doc(`Name of the environment variable (e.g., "HOST").`),
				dagql.Arg("value").Doc(`Value of the environment variable. (e.g., "localhost").`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value according to the current `+
					`environment variables defined in the container (e.g. "/opt/bin:$PATH").`),
			),

		dagql.NodeFunc("withEnvFileVariables", s.withEnvFileVariables).
			Doc(`Export environment variables from an env-file to the container.`).
			Args(
				dagql.Arg("source").Doc(`Identifier of the envfile`),
			),

		// NOTE: this is internal-only for now (hidden from codegen via the __ prefix) as we
		// currently only want to use it for allowing the Go SDK to inherit custom GOPROXY
		// settings from the engine container. It may be made public in the future with more
		// refined design.
		dagql.NodeFunc("__withSystemEnvVariable", s.withSystemEnvVariable).
			Doc(`(Internal-only) Inherit this environment variable from the engine container if set there with a special prefix.`),

		// NOTE: this is internal-only (hidden from codegen via the __ prefix). It exists so
		// llbtodagger can faithfully apply Docker image config metadata fields that do not yet
		// have public SDK methods.
		dagql.NodeFunc("__withImageConfigMetadata", func(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithImageConfigMetadataArgs) (*core.Container, error) {
			ctr, err := core.NewContainerChild(ctx, parent)
			if err != nil {
				return nil, err
			}
			return s.withImageConfigMetadata(ctx, ctr, args)
		}).
			Doc(`(Internal-only) Set Docker image config metadata fields not yet exposed as public container APIs.`).
			Args(
				dagql.Arg("healthcheck").Doc(`JSON-encoded Docker HealthcheckConfig.`),
				dagql.Arg("onBuild").Doc(`Docker ONBUILD trigger list.`),
				dagql.Arg("shell").Doc(`Docker shell override for shell-form instructions.`),
				dagql.Arg("volumes").Doc(`Docker image config volume mountpoints.`),
				dagql.Arg("stopSignal").Doc(`Docker image config stop signal.`),
			),

		dagql.NodeFunc("withSecretVariable", s.withSecretVariable).
			Doc(`Set a new environment variable, using a secret value`).
			Args(
				dagql.Arg("name").Doc(`Name of the secret variable (e.g., "API_SECRET").`),
				dagql.Arg("secret").Doc(`Identifier of the secret value.`),
			),

		dagql.NodeFunc("withoutEnvVariable", s.withoutEnvVariable).
			Doc(`Retrieves this container minus the given environment variable.`).
			Args(
				dagql.Arg("name").Doc(`The name of the environment variable (e.g., "HOST").`),
			),

		dagql.NodeFunc("withoutSecretVariable", s.withoutSecretVariable).
			Doc(`Retrieves this container minus the given environment variable containing the secret.`).
			Args(
				dagql.Arg("name").Doc(`The name of the environment variable (e.g., "HOST").`),
			),

		dagql.NodeFunc("withLabel", s.withLabel).
			Doc(`Retrieves this container plus the given label.`).
			Args(
				dagql.Arg("name").Doc(`The name of the label (e.g., "org.opencontainers.artifact.created").`),
				dagql.Arg("value").Doc(`The value of the label (e.g., "2023-01-01T00:00:00Z").`),
			),

		dagql.Func("label", s.label).
			Doc(`Retrieves the value of the specified label.`).
			Args(
				dagql.Arg("name").Doc(`The name of the label (e.g., "org.opencontainers.artifact.created").`),
			),

		dagql.Func("labels", s.labels).
			Doc(`Retrieves the list of labels passed to container.`),

		dagql.NodeFunc("withoutLabel", s.withoutLabel).
			Doc(`Retrieves this container minus the given environment label.`).
			Args(
				dagql.Arg("name").Doc(`The name of the label to remove (e.g., "org.opencontainers.artifact.created").`),
			),

		dagql.NodeFunc("withDockerHealthcheck", func(ctx context.Context, parent dagql.ObjectResult[*core.Container], args WithHealthcheckArgs) (*core.Container, error) {
			ctr, err := core.NewContainerChild(ctx, parent)
			if err != nil {
				return nil, err
			}
			return s.withHealthcheck(ctx, ctr, args)
		}).
			Doc(`Retrieves this container with the specificed docker healtcheck command set.`).
			Args(
				dagql.Arg("args").Doc(`Healthcheck command to execute. Example: ["go", "run", "main.go"].`),
				dagql.Arg("shell").Doc(`When true, command must be a single element, which is run using the container's shell`),
				dagql.Arg("interval").Doc(`Interval between running healthcheck. Example: "30s"`),
				dagql.Arg("timeout").Doc(`Healthcheck timeout. Example: "3s"`),
				dagql.Arg("startPeriod").Doc(`StartPeriod allows for failures during this initial startup period which do not count towards maximum number of retries. Example: "0s"`),
				dagql.Arg("startInterval").Doc(`StartInterval configures the duration between checks during the startup phase. Example: "5s"`),
				dagql.Arg("retries").Doc(`The maximum number of consecutive failures before the container is marked as unhealthy. Example: "3"`),
			),

		dagql.NodeFunc("withoutDockerHealthcheck", func(ctx context.Context, parent dagql.ObjectResult[*core.Container], args struct{}) (*core.Container, error) {
			ctr, err := core.NewContainerChild(ctx, parent)
			if err != nil {
				return nil, err
			}
			return s.withoutHealthcheck(ctx, ctr, args)
		}).
			Doc(`Retrieves this container without a configured docker healtcheck command.`),

		dagql.Func("dockerHealthcheck", s.healthcheck).
			Doc(`Retrieves this container's configured docker healthcheck.`),

		dagql.Func("entrypoint", s.entrypoint).
			Doc(`Return the container's OCI entrypoint.`),

		dagql.NodeFunc("withEntrypoint", s.withEntrypoint).
			Doc(`Set an OCI-style entrypoint. It will be included in the container's OCI configuration. Note, withExec ignores the entrypoint by default.`).
			Args(
				dagql.Arg("args").Doc(`Arguments of the entrypoint. Example: ["go", "run"].`),
				dagql.Arg("keepDefaultArgs").Doc(`Don't reset the default arguments when setting the entrypoint. By default it is reset, since entrypoint and default args are often tightly coupled.`),
			),

		dagql.NodeFunc("withoutEntrypoint", s.withoutEntrypoint).
			Doc(`Reset the container's OCI entrypoint.`).
			Args(
				dagql.Arg("keepDefaultArgs").Doc(`Don't remove the default arguments when unsetting the entrypoint.`),
			),

		dagql.Func("defaultArgs", s.defaultArgs).
			Doc(`Return the container's default arguments.`),

		dagql.NodeFunc("withDefaultArgs", s.withDefaultArgs).
			Doc(`Configures default arguments for future commands. Like CMD in Dockerfile.`).
			Args(
				dagql.Arg("args").Doc(`Arguments to prepend to future executions (e.g., ["-v", "--no-cache"]).`),
			),

		dagql.NodeFunc("withoutDefaultArgs", s.withoutDefaultArgs).
			Doc(`Remove the container's default arguments.`),

		dagql.Func("mounts", s.mounts).
			Doc(`Retrieves the list of paths where a directory is mounted.`),

		dagql.NodeFunc("withMountedDirectory", s.withMountedDirectory).
			Doc(`Retrieves this container plus a directory mounted at the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location of the mounted directory (e.g., "/mnt/directory").`),
				dagql.Arg("source").Doc(`Identifier of the mounted directory.`),
				dagql.Arg("owner").Doc(`A user:group to set for the mounted directory and its contents.`,
					`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
					`If the group is omitted, it defaults to the same as the user.`),
				dagql.Arg("readOnly").Doc(`Mount the directory read-only.`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
					`environment variables defined in the container (e.g. "/$VAR/foo").`),
			),

		dagql.NodeFunc("withMountedFile", s.withMountedFile).
			Doc(`Retrieves this container plus a file mounted at the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location of the mounted file (e.g., "/tmp/file.txt").`),
				dagql.Arg("source").Doc(`Identifier of the mounted file.`),
				dagql.Arg("owner").Doc(`A user or user:group to set for the mounted file.`,
					`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
					`If the group is omitted, it defaults to the same as the user.`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
					`environment variables defined in the container (e.g. "/$VAR/foo.txt").`),
			),

		dagql.NodeFunc("withMountedTemp", s.withMountedTemp).
			Doc(`Retrieves this container plus a temporary directory mounted at the given path. Any writes will be ephemeral to a single withExec call; they will not be persisted to subsequent withExecs.`).
			Args(
				dagql.Arg("path").Doc(`Location of the temporary directory (e.g., "/tmp/temp_dir").`),
				dagql.Arg("size").Doc(`Size of the temporary directory in bytes.`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
					`environment variables defined in the container (e.g. "/$VAR/foo").`),
			),

		dagql.NodeFuncWithDynamicInputs("withMountedCache", s.withMountedCache, s.withMountedCacheDynamicInputs).
			Doc(`Retrieves this container plus a cache volume mounted at the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location of the cache directory (e.g., "/root/.npm").`),
				dagql.Arg("cache").Doc(`Identifier of the cache volume to mount.`),
				dagql.Arg("source").Doc(`Identifier of the directory to use as the cache volume's root.`),
				dagql.Arg("sharing").Doc(`Sharing mode of the cache volume.`),
				dagql.Arg("owner").Doc(`A user:group to set for the mounted cache directory.`,
					`Note that this changes the ownership of the specified mount along with
					the initial filesystem provided by source (if any). It does not have
					any effect if/when the cache has already been created.`,
					`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
					`If the group is omitted, it defaults to the same as the user.`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
					`environment variables defined in the container (e.g. "/$VAR/foo").`),
			),

		dagql.NodeFunc("withMountedSecret", s.withMountedSecret).
			Doc(`Retrieves this container plus a secret mounted into a file at the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location of the secret file (e.g., "/tmp/secret.txt").`),
				dagql.Arg("source").Doc(`Identifier of the secret to mount.`),
				dagql.Arg("owner").Doc(`A user:group to set for the mounted secret.`,
					`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
					`If the group is omitted, it defaults to the same as the user.`),
				dagql.Arg("mode").Doc(`Permission given to the mounted secret (e.g., 0600).`,
					`This option requires an owner to be set to be active.`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
					`environment variables defined in the container (e.g. "/$VAR/foo").`),
			),

		dagql.NodeFunc("withUnixSocket", s.withUnixSocket).
			Doc(`Retrieves this container plus a socket forwarded to the given Unix socket path.`).
			Args(
				dagql.Arg("path").Doc(`Location of the forwarded Unix socket (e.g., "/tmp/socket").`),
				dagql.Arg("source").Doc(`Identifier of the socket to forward.`),
				dagql.Arg("owner").Doc(`A user:group to set for the mounted socket.`,
					`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
					`If the group is omitted, it defaults to the same as the user.`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
					`environment variables defined in the container (e.g. "/$VAR/foo").`),
			),

		dagql.NodeFunc("withoutUnixSocket", s.withoutUnixSocket).
			Doc(`Retrieves this container with a previously added Unix socket removed.`).
			Args(
				dagql.Arg("path").Doc(`Location of the socket to remove (e.g., "/tmp/socket").`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
					`environment variables defined in the container (e.g. "/$VAR/foo").`),
			),

		dagql.NodeFunc("withoutMount", s.withoutMount).
			Doc(`Retrieves this container after unmounting everything at the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location of the cache directory (e.g., "/root/.npm").`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
					`environment variables defined in the container (e.g. "/$VAR/foo").`),
			),

		dagql.NodeFunc("withFile", s.withFile).
			IsPersistable().
			Doc(`Return a container snapshot with a file added`).
			Args(
				dagql.Arg("path").Doc(`Path of the new file. Example: "/path/to/new-file.txt"`),
				dagql.Arg("source").Doc(`File to add`),
				dagql.Arg("permissions").Doc(`Permissions of the new file. Example: 0600`),
				dagql.Arg("owner").Doc(`A user:group to set for the file.`,
					`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
					`If the group is omitted, it defaults to the same as the user.`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
					`environment variables defined in the container (e.g. "/$VAR/foo.txt").`),
			),

		dagql.NodeFunc("withoutFile", s.withoutFile).
			IsPersistable().
			Doc(`Retrieves this container with the file at the given path removed.`).
			Args(
				dagql.Arg("path").Doc(`Location of the file to remove (e.g., "/file.txt").`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
					`environment variables defined in the container (e.g. "/$VAR/foo.txt").`),
			),

		dagql.NodeFunc("withoutFiles", s.withoutFiles).
			IsPersistable().
			Doc(`Return a new container spanshot with specified files removed`).
			Args(
				dagql.Arg("paths").Doc(`Paths of the files to remove. Example: ["foo.txt, "/root/.ssh/config"`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value of paths according to the current `+
					`environment variables defined in the container (e.g. "/$VAR/foo.txt").`),
			),

		dagql.NodeFunc("withFiles", s.withFiles).
			IsPersistable().
			Doc(`Retrieves this container plus the contents of the given files copied to the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location where copied files should be placed (e.g., "/src").`),
				dagql.Arg("sources").Doc(`Identifiers of the files to copy.`),
				dagql.Arg("permissions").Doc(`Permission given to the copied files (e.g., 0600).`),
				dagql.Arg("owner").Doc(`A user:group to set for the files.`,
					`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
					`If the group is omitted, it defaults to the same as the user.`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
					`environment variables defined in the container (e.g. "/$VAR/foo.txt").`),
			),

		dagql.NodeFunc("withNewFile", s.withNewFile).
			IsPersistable().
			View(AllVersion).
			Doc(`Return a new container snapshot, with a file added to its filesystem with text content`).
			Args(
				dagql.Arg("path").Doc(`Path of the new file. May be relative or absolute. Example: "README.md" or "/etc/profile"`),
				dagql.Arg("contents").Doc(`Contents of the new file. Example: "Hello world!"`),
				dagql.Arg("permissions").Doc(`Permissions of the new file. Example: 0600`),
				dagql.Arg("owner").Doc(
					`A user:group to set for the file.`,
					`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
					`If the group is omitted, it defaults to the same as the user.`),
				dagql.Arg("expand").Doc(
					`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
						`environment variables defined in the container (e.g. "/$VAR/foo.txt").`),
			),
		dagql.NodeFunc("withNewFile", s.withNewFileLegacy).
			View(BeforeVersion("v0.12.0")).
			Doc(`Retrieves this container plus a new file written at the given path.`).
			Args(
				dagql.Arg("path").Doc(`Location of the written file (e.g., "/tmp/file.txt").`),
				dagql.Arg("contents").Doc(`Content of the file to write (e.g., "Hello world!").`),
				dagql.Arg("permissions").Doc(`Permission given to the written file (e.g., 0600).`),
				dagql.Arg("owner").Doc(`A user:group to set for the file.`,
					`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
					`If the group is omitted, it defaults to the same as the user.`),
			),

		dagql.NodeFunc("withDirectory", s.withDirectory).
			IsPersistable().
			View(AllVersion).
			Doc(`Return a new container snapshot, with a directory added to its filesystem`).
			Args(
				dagql.Arg("path").Doc(`Location of the written directory (e.g., "/tmp/directory").`),
				dagql.Arg("directory").Doc(`Identifier of the directory to write`).View(BeforeVersion("v0.19.0")),
				dagql.Arg("source").Doc(`Identifier of the directory to write`).View(AfterVersion("v0.19.0")),
				dagql.Arg("exclude").Doc(`Patterns to exclude in the written directory (e.g. ["node_modules/**", ".gitignore", ".git/"]).`),
				dagql.Arg("include").Doc(`Patterns to include in the written directory (e.g. ["*.go", "go.mod", "go.sum"]).`),
				dagql.Arg("gitignore").Doc(`Apply .gitignore rules when writing the directory.`),
				dagql.Arg("owner").Doc(`A user:group to set for the directory and its contents.`,
					`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
					`If the group is omitted, it defaults to the same as the user.`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
					`environment variables defined in the container (e.g. "/$VAR/foo").`),
			),

		dagql.NodeFunc("withoutDirectory", s.withoutDirectory).
			IsPersistable().
			Doc(`Return a new container snapshot, with a directory removed from its filesystem`).
			Args(
				dagql.Arg("path").Doc(`Location of the directory to remove (e.g., ".github/").`),
				dagql.Arg("expand").Doc(`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
					`environment variables defined in the container (e.g. "/$VAR/foo").`),
			),

		dagql.NodeFunc("exists", s.exists).
			Doc(`check if a file or directory exists`).
			Args(
				dagql.Arg("path").Doc(`Path to check (e.g., "/file.txt").`),
				dagql.Arg("expectedType").Doc(`If specified, also validate the type of file (e.g. "REGULAR_TYPE", "DIRECTORY_TYPE", or "SYMLINK_TYPE").`),
				dagql.Arg("doNotFollowSymlinks").Doc(`If specified, do not follow symlinks.`),
			),

		dagql.NodeFunc("stat", s.stat).
			Doc(`Return file status`).
			Args(
				dagql.Arg("path").Doc(`Path to check (e.g., "/file.txt").`),
				dagql.Arg("doNotFollowSymlinks").Doc(`If specified, do not follow symlinks.`),
			),

		dagql.NodeFunc("withError", s.withError).
			Doc(`Raise an error.`).
			Args(
				dagql.Arg("err").Doc(`Message of the error to raise. If empty, the error will be ignored.`),
			),

		dagql.NodeFunc("withExec", s.withExec).
			IsPersistable().
			View(AllVersion).
			Doc(`Execute a command in the container, and return a new snapshot of the container state after execution.`).
			Args(
				dagql.Arg("args").Doc(
					`Command to execute. Must be valid exec() arguments, not a shell command. Example: ["go", "run", "main.go"].`,
					`To run a shell command, execute the shell and pass the shell command as argument. Example: ["sh", "-c", "ls -l | grep foo"]`,
					`Defaults to the container's default arguments (see "defaultArgs" and "withDefaultArgs").`),
				dagql.Arg("useEntrypoint").
					View(AllVersion).
					Doc(`Apply the OCI entrypoint, if present, by prepending it to the args. Ignored by default.`),
				dagql.Arg("useEntrypoint").
					View(BeforeVersion("v0.12.0")).
					Default(dagql.Boolean(true)).
					Doc(`Apply the OCI entrypoint, if present, by prepending it to the args. Ignored by default.`),
				dagql.Arg("skipEntrypoint").
					View(BeforeVersion("v0.13.0")).
					Doc("For true this can be removed. For false, use `useEntrypoint` instead."),
				dagql.Arg("stdin").Doc(
					`Content to write to the command's standard input. Example: "Hello world")`),
				dagql.Arg("redirectStdin").Doc(
					`Redirect the command's standard input from a file in the container. Example: "./stdin.txt"`),
				dagql.Arg("redirectStdout").Doc(
					`Redirect the command's standard output to a file in the container. Example: "./stdout.txt"`),
				dagql.Arg("redirectStderr").Doc(
					`Redirect the command's standard error to a file in the container. Example: "./stderr.txt"`),
				dagql.Arg("expect").Doc(`Exit codes this command is allowed to exit with without error`),
				dagql.Arg("experimentalPrivilegedNesting").Doc(
					`Provides Dagger access to the executed command.`),
				dagql.Arg("expect").Doc(`Exit codes this command is allowed to exit with without error`),
				dagql.Arg("experimentalPrivilegedNesting").Doc(
					`Provides Dagger access to the executed command.`),
				dagql.Arg("insecureRootCapabilities").Doc(
					`Execute the command with all root capabilities. Like --privileged in Docker`,
					`DANGER: this grants the command full access to the host system. Only use when 1) you trust the command being executed and 2) you specifically need this level of access.`),
				dagql.Arg("expand").Doc(
					`Replace "${VAR}" or "$VAR" in the args according to the current `+
						`environment variables defined in the container (e.g. "/$VAR/foo").`),
				dagql.Arg("noInit").Doc(
					`Skip the automatic init process injected into containers by default.`,
					`Only use this if you specifically need the command to be pid 1 in the container. Otherwise it may result in unexpected behavior. If you're not sure, you don't need this.`,
				),
			),

		dagql.NodeFunc("stdout", s.stdout).
			View(AllVersion).
			Doc(`The buffered standard output stream of the last executed command`,
				`Returns an error if no command was executed`),
		dagql.NodeFunc("stdout", s.stdoutLegacy).
			View(BeforeVersion("v0.12.0")).
			Extend(),

		dagql.NodeFunc("stderr", s.stderr).
			View(AllVersion).
			Doc(`The buffered standard error stream of the last executed command`,
				`Returns an error if no command was executed`),
		dagql.NodeFunc("stderr", s.stderrLegacy).
			View(BeforeVersion("v0.12.0")).
			Extend(),

		dagql.NodeFunc("combinedOutput", s.combinedOutput).
			Doc(`The combined buffered standard output and standard error stream of the last executed command`,
				`Returns an error if no command was executed`),

		dagql.NodeFunc("exitCode", s.exitCode).
			Doc(`The exit code of the last executed command`,
				`Returns an error if no command was executed`),

		dagql.NodeFunc("withSymlink", s.withSymlink).
			IsPersistable().
			Doc(`Return a snapshot with a symlink`).
			Args(
				dagql.Arg("target").Doc(`Location of the file or directory to link to (e.g., "/existing/file").`),
				dagql.Arg("linkName").Doc(`Location where the symbolic link will be created (e.g., "/new-file-link").`),
				dagql.Arg("expand").Doc(
					`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
						`environment variables defined in the container (e.g. "/$VAR/foo.txt").`),
			),

		dagql.NodeFunc("withAnnotation", s.withAnnotation).
			Doc(`Retrieves this container plus the given OCI annotation.`).
			Args(
				dagql.Arg("name").Doc(`The name of the annotation.`),
				dagql.Arg("value").Doc(`The value of the annotation.`),
			),

		dagql.NodeFunc("withoutAnnotation", s.withoutAnnotation).
			Doc(`Retrieves this container minus the given OCI annotation.`).
			Args(
				dagql.Arg("name").Doc(`The name of the annotation.`),
			),

		dagql.NodeFunc("publish", s.publish).
			WithInput(dagql.PerCallInput).
			DoNotCache("side effect on an external system (OCI registry)").
			Doc(`Package the container state as an OCI image, and publish it to a registry`,
				`Returns the fully qualified address of the published image, with digest`).
			Args(
				dagql.Arg("address").Doc(
					`The OCI address to publish to`,
					`Same format as "docker push". Example: "registry.example.com/user/repo:tag"`),
				dagql.Arg("platformVariants").Doc(
					`Identifiers for other platform specific containers.`,
					`Used for multi-platform image.`),
				dagql.Arg("forcedCompression").Doc(
					`Force each layer of the published image to use
					the specified compression algorithm.`,
					`If this is unset, then if a layer already has a compressed blob in the
					engine's cache, that will be used (this can result in a mix of
					compression algorithms for different layers). If this is unset and a
					layer has no compressed blob in the engine's cache, then it will be
					compressed using Gzip.`),
				dagql.Arg("mediaTypes").Doc(
					`Use the specified media types for the published image's layers.`,
					`Defaults to "OCI", which is compatible with most recent
				registries, but "Docker" may be needed for older registries without OCI
				support.`),
			),

		dagql.Func("platform", s.platform).
			Doc(`The platform this container executes and publishes as.`),

		dagql.NodeFunc("export", s.export).
			WithInput(dagql.PerCallInput).
			View(AllVersion).
			DoNotCache("Writes to the local host.").
			Doc(`Writes the container as an OCI tarball to the destination file path on the host.`,
				`It can also export platform variants.`).
			Args(
				dagql.Arg("path").Doc(
					`Host's destination path (e.g., "./tarball").`,
					`Path can be relative to the engine's workdir or absolute.`),
				dagql.Arg("platformVariants").Doc(
					`Identifiers for other platform specific containers.`,
					`Used for multi-platform image.`),
				dagql.Arg("forcedCompression").Doc(
					`Force each layer of the exported image to use the specified compression algorithm.`,
					`If this is unset, then if a layer already has a compressed blob in the
					engine's cache, that will be used (this can result in a mix of
					compression algorithms for different layers). If this is unset and a
					layer has no compressed blob in the engine's cache, then it will be
					compressed using Gzip.`),
				dagql.Arg("mediaTypes").Doc(
					`Use the specified media types for the exported image's layers.`,
					`Defaults to OCI, which is largely compatible with most recent
					container runtimes, but Docker may be needed for older runtimes without
					OCI support.`),
				dagql.Arg("expand").Doc(
					`Replace "${VAR}" or "$VAR" in the value of path according to the current `+
						`environment variables defined in the container (e.g. "/$VAR/foo").`),
			),
		dagql.NodeFunc("export", s.exportLegacy).
			WithInput(dagql.PerCallInput).
			View(BeforeVersion("v0.12.0")).
			Extend(),

		dagql.NodeFunc("exportImage", s.exportImage).
			DoNotCache("Writes to the local host.").
			Doc("Exports the container as an image to the host's container image store.").
			Args(
				dagql.Arg("name").Doc("Name of image to export to in the host's store"),
				dagql.Arg("platformVariants").Doc(
					`Identifiers for other platform specific containers.`,
					`Used for multi-platform image.`),
				dagql.Arg("forcedCompression").Doc(
					`Force each layer of the exported image to use the specified compression algorithm.`,
					`If this is unset, then if a layer already has a compressed blob in the
					engine's cache, that will be used (this can result in a mix of
					compression algorithms for different layers). If this is unset and a
					layer has no compressed blob in the engine's cache, then it will be
					compressed using Gzip.`),
				dagql.Arg("mediaTypes").Doc(
					`Use the specified media types for the exported image's layers.`,
					`Defaults to OCI, which is largely compatible with most recent
					container runtimes, but Docker may be needed for older runtimes without
					OCI support.`),
			),

		dagql.NodeFunc("asTarball", s.asTarball).
			IsPersistable().
			Doc(`Package the container state as an OCI image, and return it as a tar archive`).
			Args(
				dagql.Arg("platformVariants").Doc(
					`Identifiers for other platform specific containers.`,
					`Used for multi-platform images.`),
				dagql.Arg("forcedCompression").Doc(
					`Force each layer of the image to use the specified compression algorithm.`,
					`If this is unset, then if a layer already has a compressed blob in the
					engine's cache, that will be used (this can result in a mix of
					compression algorithms for different layers). If this is unset and a
					layer has no compressed blob in the engine's cache, then it will be
					compressed using Gzip.`),
				dagql.Arg("mediaTypes").Doc(`Use the specified media types for the image's layers.`,
					`Defaults to OCI, which is largely compatible with most recent
					container runtimes, but Docker may be needed for older runtimes without
					OCI support.`),
			),

		dagql.NodeFunc("import", s.import_).
			Doc(`Reads the container from an OCI tarball.`).
			Args(
				dagql.Arg("source").Doc(`File to read the container from.`),
				dagql.Arg("tag").Doc(`Identifies the tag to import from the archive, if the archive bundles multiple tags.`),
			),

		dagql.Func("withRegistryAuth", s.withRegistryAuth).
			Doc(`Attach credentials for future publishing to a registry. Use in combination with publish`).
			Args(
				dagql.Arg("address").Doc(`The image address that needs authentication. Same format as "docker push". Example: "registry.dagger.io/dagger:latest"`),
				dagql.Arg("username").Doc(`The username to authenticate with. Example: "alice"`),
				dagql.Arg("secret").Doc(`The API key, password or token to authenticate to this registry`),
			),

		dagql.Func("withoutRegistryAuth", s.withoutRegistryAuth).
			Doc(`Retrieves this container without the registry authentication of a given address.`).
			Args(
				dagql.Arg("address").Doc(`Registry's address to remove the authentication from.`,
					`Formatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main).`),
			),

		dagql.Func("imageRef", s.imageRef).
			Doc(`The unique image reference which can only be retrieved immediately after the 'Container.From' call.`),

		dagql.NodeFunc("withExposedPort", s.withExposedPort).
			Doc(`Expose a network port. Like EXPOSE in Dockerfile (but with healthcheck support)`,
				`Exposed ports serve two purposes:`,
				`- For health checks and introspection, when running services`,
				`- For setting the EXPOSE OCI field when publishing the container`).
			Args(
				dagql.Arg("port").Doc(`Port number to expose. Example: 8080`),
				dagql.Arg("protocol").Doc(`Network protocol. Example: "tcp"`),
				dagql.Arg("description").Doc(`Port description. Example: "payment API endpoint"`),
				dagql.Arg("experimentalSkipHealthcheck").Doc(`Skip the health check when run as a service.`),
			),

		dagql.NodeFunc("withoutExposedPort", s.withoutExposedPort).
			Doc(`Unexpose a previously exposed port.`).
			Args(
				dagql.Arg("port").Doc(`Port number to unexpose`),
				dagql.Arg("protocol").Doc(`Port protocol to unexpose`),
			),

		dagql.Func("exposedPorts", s.exposedPorts).
			Doc(`Retrieves the list of exposed ports.`,
				`This includes ports already exposed by the image, even if not explicitly added with dagger.`),

		dagql.NodeFunc("withServiceBinding", s.withServiceBinding).
			Doc(`Establish a runtime dependency from a container to a network service.`,
				`The service will be started automatically when needed and detached
				when it is no longer needed, executing the default command if none is
				set.`,
				`The service will be reachable from the container via the provided hostname alias.`,
				`The service dependency will also convey to any files or directories produced by the container.`).
			Args(
				dagql.Arg("alias").Doc(`Hostname that will resolve to the target service (only accessible from within this container)`),
				dagql.Arg("service").Doc(`The target service`),
			),

		dagql.Func("withFocus", s.withFocus).
			View(BeforeVersion("v0.13.4")).
			Doc(`Indicate that subsequent operations should be featured more prominently in the UI.`),
		dagql.Func("withoutFocus", s.withoutFocus).
			View(BeforeVersion("v0.13.4")).
			Doc(`Indicate that subsequent operations should not be featured more prominently in the UI.`,
				`This is the initial state of all containers.`),

		dagql.NodeFunc("withDefaultTerminalCmd", s.withDefaultTerminalCmd).
			Doc(`Set the default command to invoke for the container's terminal API.`).
			Args(
				dagql.Arg("args").Doc(`The args of the command.`),
				dagql.Arg("experimentalPrivilegedNesting").Doc(
					`Provides Dagger access to the executed command.`),
				dagql.Arg("insecureRootCapabilities").Doc(
					`Execute the command with all root capabilities. This is similar to
				running a command with "sudo" or executing "docker run" with the
				"--privileged" flag. Containerization does not provide any security
				guarantees when using this option. It should only be used when
				absolutely necessary and only with trusted commands.`),
			),

		dagql.NodeFunc("terminal", s.terminal).
			View(AfterVersion("v0.12.0")).
			DoNotCache("Only creates a temporary container for the user to interact with and then returns original parent.").
			Doc(`Opens an interactive terminal for this container using its configured default terminal command if not overridden by args (or sh as a fallback default).`).
			Args(
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
		dagql.NodeFunc("terminal", s.terminalLegacy).
			View(BeforeVersion("v0.12.0")).
			Doc(`Opens an interactive terminal for this container using its configured default terminal command if not overridden by args (or sh as a fallback default).`).
			Args(
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

		dagql.NodeFunc("experimentalWithGPU", s.withGPU).
			Doc(`EXPERIMENTAL API! Subject to change/removal at any time.`,
				`Configures the provided list of devices to be accessible to this container.`,
				`This currently works for Nvidia devices only.`).
			Args(
				dagql.Arg("devices").Doc(`List of devices to be accessible to this container.`),
			),

		dagql.NodeFunc("experimentalWithAllGPUs", s.withAllGPUs).
			Doc(`EXPERIMENTAL API! Subject to change/removal at any time.`,
				`Configures all available GPUs on the host to be accessible to this container.`,
				`This currently works for Nvidia devices only.`),
	}.Install(srv)

	dagql.Fields[*core.TerminalLegacy]{
		Syncer[*core.TerminalLegacy]().
			Doc(`Forces evaluation of the pipeline in the engine.`,
				`It doesn't run the default command if no exec has been set.`),

		dagql.Func("websocketEndpoint", s.terminalLegacyWebsocketEndpoint).
			View(BeforeVersion("v0.12.0")).
			Deprecated("Use newer dagger to access the terminal").
			Doc(`An http endpoint at which this terminal can be connected to over a websocket.`),
	}.Install(srv)
}

type containerArgs struct {
	Platform dagql.Optional[core.Platform]
}

func (s *containerSchema) container(ctx context.Context, parent *core.Query, args containerArgs) (_ *core.Container, rerr error) {
	var platform core.Platform
	if args.Platform.Valid {
		platform = args.Platform.Value
	} else {
		platform = parent.Platform()
	}
	return core.NewContainer(platform), nil
}

type containerFromArgs struct {
	Address string
}

// if the image ref has a digest, then it's immutable and we don't need to scope it to the session. If it's just a tag, then
// we scope to the session so that resolution of a tag->digest is cached within the session but not across.
var fromSessionScopeInput = dagql.ImplicitInput{
	Name: "fromSessionScope",
	Resolver: func(ctx context.Context, args map[string]dagql.Input) (dagql.Input, error) {
		rawAddress, ok := args["address"]
		if !ok || rawAddress == nil {
			return nil, errors.New("missing required address argument")
		}
		address, ok := rawAddress.(dagql.String)
		if !ok {
			return nil, fmt.Errorf("unexpected address input type %T", rawAddress)
		}

		refName, err := reference.ParseNormalizedNamed(address.String())
		if err != nil {
			return nil, fmt.Errorf("failed to parse image address %s: %w", address.String(), err)
		}
		refName = reference.TagNameOnly(refName)
		if _, isCanonical := refName.(reference.Canonical); isCanonical {
			// Digest-addressed refs are immutable and don't need session scoping.
			return dagql.NewString(""), nil
		}

		clientMD, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get client metadata: %w", err)
		}
		if clientMD.SessionID == "" {
			return nil, errors.New("session ID not found in context")
		}
		// Tag-only refs are mutable; resolve once per session.
		return dagql.NewString(clientMD.SessionID), nil
	},
}

func (s *containerSchema) from(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerFromArgs) (inst dagql.ObjectResult[*core.Container], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dagql server: %w", err)
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	rslvr, err := query.RegistryResolver(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get registry resolver: %w", err)
	}
	platform := parent.Self().Platform

	refName, err := reference.ParseNormalizedNamed(args.Address)
	if err != nil {
		return inst, fmt.Errorf("failed to parse image address %s: %w", args.Address, err)
	}
	// add a default :latest if no tag or digest, otherwise this is a no-op
	refName = reference.TagNameOnly(refName)

	if refName, isCanonical := refName.(reference.Canonical); isCanonical {
		ctr, err := core.NewContainerChildWithoutFS(ctx, parent)
		if err != nil {
			return inst, err
		}
		ctr.ImageRef = ""

		refStr := refName.String()
		_, _, cfgBytes, err := rslvr.ResolveImageConfig(ctx, refStr, serverresolver.ResolveImageConfigOpts{
			Platform:    ptr(platform.Spec()),
			ResolveMode: serverresolver.ResolveModeDefault,
		})
		if err != nil {
			return inst, fmt.Errorf("failed to resolve image %q (platform: %q): %w", refStr, platform.Format(), err)
		}

		var imgSpec dockerspec.DockerOCIImage
		if err := json.Unmarshal(cfgBytes, &imgSpec); err != nil {
			return inst, err
		}

		ctr.Config = core.MergeImageConfig(ctr.Config, imgSpec.Config)
		ctr.ImageRef = refStr
		ctr.Platform = core.Platform(platforms.Normalize(imgSpec.Platform))

		rootfsDir := &core.Directory{
			Dir:      "/",
			Platform: ctr.Platform,
			Services: ctr.Services,
			Lazy:     &core.DirectoryFromContainerLazy{Container: ctr},
		}
		ctr.FS = &core.ContainerDirectorySource{Value: rootfsDir}
		ctr.Lazy = &core.ContainerFromImageRefLazy{
			LazyState:    core.NewLazyState(),
			CanonicalRef: refStr,
			ResolveMode:  serverresolver.ResolveModeDefault,
		}

		inst, err = dagql.NewObjectResultForCurrentCall(ctx, srv, ctr)
		if err != nil {
			return inst, err
		}

		// detach identity from the :tag, make the result purely content-addressed based on the digest
		return inst.WithContentDigest(ctx, hashutil.HashStrings(
			"container.from",
			refName.Digest().String(),
			ctr.Platform.Format(),
		))
	}

	// Doesn't have a digest, resolve that now and re-call this field using the canonical
	// digested ref instead. This ensures the ID returned here is always stable w/ the
	// digested image ref.
	_, digest, _, err := rslvr.ResolveImageConfig(ctx, refName.String(), serverresolver.ResolveImageConfigOpts{
		Platform:    ptr(platform.Spec()),
		ResolveMode: serverresolver.ResolveModeDefault,
	})
	if err != nil {
		return inst, fmt.Errorf("failed to resolve image %q (platform: %q): %w", refName.String(), platform.Format(), err)
	}
	refName, err = reference.WithDigest(refName, digest)
	if err != nil {
		return inst, fmt.Errorf("failed to set digest on image %s: %w", refName.String(), err)
	}

	ctx, span := core.Tracer(ctx).Start(ctx, fmt.Sprintf("from %s", refName),
		telemetry.Internal(),
	)
	defer telemetry.EndWithCause(span, nil)

	err = srv.Select(ctx, parent, &inst,
		dagql.Selector{
			Field: "from",
			Args: []dagql.NamedInput{
				{Name: "address", Value: dagql.String(refName.String())},
			},
		},
	)
	if err != nil {
		return inst, err
	}

	return inst, nil
}

type containerBuildArgs struct {
	Context    core.DirectoryID
	Dockerfile string                             `default:"Dockerfile"`
	Target     string                             `default:""`
	BuildArgs  []dagql.InputObject[core.BuildArg] `default:"[]"`
	Secrets    []core.SecretID                    `default:"[]"`
	NoInit     bool                               `default:"false"`
}

func (s *containerSchema) build(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Container],
	args containerBuildArgs,
) (*core.Container, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	srv, err := query.Server.Server(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	contextDir, err := args.Context.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	buildctxDir, err := applyDockerIgnore(ctx, srv, contextDir, args.Dockerfile)
	if err != nil {
		return nil, err
	}

	secrets, err := dagql.LoadIDResults(ctx, srv, args.Secrets)
	if err != nil {
		return nil, err
	}

	buildctxDirID, err := buildctxDir.RecipeID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get build context recipe ID: %w", err)
	}

	return parent.Self().Build(
		ctx,
		contextDir.Self(),
		buildctxDirID,
		args.Dockerfile,
		collectInputsSlice(args.BuildArgs),
		args.Target,
		secrets,
		args.NoInit,
		dagql.ObjectResult[*core.Socket]{},
	)
}

type containerWithRootFSArgs struct {
	Directory core.DirectoryID
}

func (s *containerSchema) withRootfs(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithRootFSArgs) (*core.Container, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	dir, err := args.Directory.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithRootFS(ctx, dir)
}

type containerPipelineArgs struct {
	Name        string
	Description string                             `default:""`
	Labels      []dagql.InputObject[PipelineLabel] `default:"[]"`
}

func (s *containerSchema) pipeline(ctx context.Context, parent *core.Container, args containerPipelineArgs) (*core.Container, error) {
	// deprecated no-op
	return parent, nil
}

func (s *containerSchema) rootfs(ctx context.Context, parent dagql.ObjectResult[*core.Container], args struct{}) (dagql.ObjectResult[*core.Directory], error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}
	return parent.Self().RootFS(ctx)
}

type containerExecArgs struct {
	core.ContainerExecOpts

	// If the container has an entrypoint, ignore it for this exec rather than
	// calling it with args
	SkipEntrypoint *bool `default:"false"`

	// ExecMD carries internal runtime execution metadata.
	ExecMD dagql.SerializedString[*engineutil.ExecutionMetadata] `name:"execMD" internal:"true" default:"null"`
}

func (s *containerSchema) withError(ctx context.Context, parent dagql.ObjectResult[*core.Container], args struct{ Err string }) (inst dagql.ObjectResult[*core.Container], rerr error) {
	if args.Err == "" {
		return parent, nil
	}
	return inst, errors.New(args.Err)
}

func (s *containerSchema) withExec(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerExecArgs) (inst dagql.ObjectResult[*core.Container], _ error) {
	if args.Stdin != "" && args.RedirectStdin != "" {
		return inst, fmt.Errorf("cannot set both stdin and redirectStdin")
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get server: %w", err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return inst, err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return inst, err
	}

	if args.SkipEntrypoint != nil {
		args.UseEntrypoint = !*args.SkipEntrypoint
	}

	expandedArgs := make([]string, len(args.Args))
	for i, arg := range args.Args {
		expandedArg, err := expandEnvVar(ctx, parent.Self(), arg, args.Expand)
		if err != nil {
			return inst, err
		}

		expandedArgs[i] = expandedArg
	}
	args.Args = expandedArgs

	if args.RedirectStdout != "" {
		args.RedirectStdout, err = expandEnvVar(ctx, parent.Self(), args.RedirectStdout, args.Expand)
		if err != nil {
			return inst, err
		}
	}
	if args.RedirectStderr != "" {
		args.RedirectStderr, err = expandEnvVar(ctx, parent.Self(), args.RedirectStderr, args.Expand)
		if err != nil {
			return inst, err
		}
	}
	if args.RedirectStdin != "" {
		args.RedirectStdin, err = expandEnvVar(ctx, parent.Self(), args.RedirectStdin, args.Expand)
		if err != nil {
			return inst, err
		}
	}

	var md *engineutil.ExecutionMetadata
	if args.ExecMD.Self != nil {
		md = args.ExecMD.Self
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return inst, err
	}
	err = ctr.WithExec(ctx, parent, args.ContainerExecOpts, md, false)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ctr)
}

func (s *containerSchema) stdout(ctx context.Context, parent dagql.ObjectResult[*core.Container], _ struct{}) (string, error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return "", err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return "", err
	}
	return parent.Self().Stdout(ctx)
}

//nolint:dupl
func (s *containerSchema) stdoutLegacy(ctx context.Context, parent dagql.ObjectResult[*core.Container], _ struct{}) (string, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get server: %w", err)
	}

	out, err := parent.Self().Stdout(ctx)
	if errors.Is(err, core.ErrNoCommand) {
		var ctr dagql.ObjectResult[*core.Container]
		if err := srv.Select(ctx, parent, &ctr, dagql.Selector{
			Field: "withExec",
			Args: []dagql.NamedInput{
				{
					Name:  "args",
					Value: dagql.ArrayInput[dagql.String]{},
				},
				{
					Name:  "useEntrypoint",
					Value: dagql.NewBoolean(true),
				},
			},
		}); err != nil {
			return "", err
		}
		return ctr.Self().Stdout(ctx)
	}
	return out, err
}

func (s *containerSchema) stderr(ctx context.Context, parent dagql.ObjectResult[*core.Container], _ struct{}) (string, error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return "", err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return "", err
	}
	return parent.Self().Stderr(ctx)
}

//nolint:dupl
func (s *containerSchema) stderrLegacy(ctx context.Context, parent dagql.ObjectResult[*core.Container], _ struct{}) (string, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get server: %w", err)
	}

	out, err := parent.Self().Stderr(ctx)
	if errors.Is(err, core.ErrNoCommand) {
		var ctr dagql.ObjectResult[*core.Container]
		if err := srv.Select(ctx, parent, &ctr, dagql.Selector{
			Field: "withExec",
			Args: []dagql.NamedInput{
				{
					Name:  "args",
					Value: dagql.ArrayInput[dagql.String]{},
				},
				{
					Name:  "useEntrypoint",
					Value: dagql.NewBoolean(true),
				},
			},
		}); err != nil {
			return "", err
		}
		return ctr.Self().Stderr(ctx)
	}
	return out, err
}

func (s *containerSchema) combinedOutput(ctx context.Context, parent dagql.ObjectResult[*core.Container], _ struct{}) (string, error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return "", err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return "", err
	}
	return parent.Self().CombinedOutput(ctx)
}

func (s *containerSchema) exitCode(ctx context.Context, parent dagql.ObjectResult[*core.Container], _ struct{}) (int, error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return 0, err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return 0, err
	}
	return parent.Self().ExitCode(ctx)
}

type containerWithSymlinkArgs struct {
	Target   string
	LinkName string
	Expand   bool `default:"false"`
}

func (s *containerSchema) withSymlink(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithSymlinkArgs) (inst dagql.ObjectResult[*core.Container], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get server: %w", err)
	}

	target, err := expandEnvVar(ctx, parent.Self(), args.Target, args.Expand)
	if err != nil {
		return inst, err
	}

	linkName, err := expandEnvVar(ctx, parent.Self(), args.LinkName, args.Expand)
	if err != nil {
		return inst, err
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return inst, err
	}
	ctr, err = ctr.WithSymlink(ctx, parent, srv, target, linkName)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ctr)
}

type containerGpuArgs struct {
	core.ContainerGPUOpts
}

func (s *containerSchema) withGPU(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerGpuArgs) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithGPU(ctx, args.ContainerGPUOpts)
}

func (s *containerSchema) withAllGPUs(ctx context.Context, parent dagql.ObjectResult[*core.Container], args struct{}) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithGPU(ctx, core.ContainerGPUOpts{Devices: []string{"all"}})
}

type containerWithEntrypointArgs struct {
	Args            []string
	KeepDefaultArgs bool `default:"false"`
}

func (s *containerSchema) withEntrypoint(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithEntrypointArgs) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.UpdateImageConfig(ctx, func(cfg dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig {
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

func (s *containerSchema) withoutEntrypoint(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithoutEntrypointArgs) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.UpdateImageConfig(ctx, func(cfg dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig {
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

func (s *containerSchema) withDefaultArgs(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithDefaultArgs) (*core.Container, error) {
	c, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	c.DefaultArgs = true
	return c.UpdateImageConfig(ctx, func(cfg dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig {
		if args.Args == nil {
			cfg.Cmd = []string{}
			return cfg
		}

		cfg.Cmd = args.Args
		return cfg
	})
}

func (s *containerSchema) withoutDefaultArgs(ctx context.Context, parent dagql.ObjectResult[*core.Container], _ struct{}) (*core.Container, error) {
	c, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	c.DefaultArgs = false
	return c.UpdateImageConfig(ctx, func(cfg dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig {
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

func (s *containerSchema) withUser(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithUserArgs) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.UpdateImageConfig(ctx, func(cfg dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig {
		cfg.User = args.Name
		return cfg
	})
}

func (s *containerSchema) withoutUser(ctx context.Context, parent dagql.ObjectResult[*core.Container], _ struct{}) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.UpdateImageConfig(ctx, func(cfg dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig {
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
	Path   string
	Expand bool `default:"false"`
}

func (s *containerSchema) withWorkdir(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithWorkdirArgs) (*core.Container, error) {
	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return nil, err
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.UpdateImageConfig(ctx, func(cfg dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig {
		cfg.WorkingDir = absPath(cfg.WorkingDir, path)
		return cfg
	})
}

func (s *containerSchema) withoutWorkdir(ctx context.Context, parent dagql.ObjectResult[*core.Container], _ struct{}) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.UpdateImageConfig(ctx, func(cfg dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig {
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

func (s *containerSchema) withEnvVariable(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithVariableArgs) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.UpdateImageConfig(ctx, func(cfg dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig {
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

type withEnvFileVariablesArgs struct {
	Source core.EnvFileID
}

func (s *containerSchema) withEnvFileVariables(ctx context.Context, parent dagql.ObjectResult[*core.Container], args withEnvFileVariablesArgs) (*core.Container, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	ef, err := args.Source.Load(ctx, srv)
	if err != nil {
		return nil, err
	}

	vars, err := ef.Self().Variables(ctx, false)
	if err != nil {
		return nil, err
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.UpdateImageConfig(ctx, func(cfg dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig {
		for _, v := range vars {
			cfg.Env = core.AddEnv(cfg.Env, v.Name, v.Value)
		}
		return cfg
	})
}

type containerWithSystemEnvArgs struct {
	Name string
}

func (s *containerSchema) withSystemEnvVariable(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithSystemEnvArgs) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	ctr.SystemEnvNames = append(ctr.SystemEnvNames, args.Name)
	return ctr, nil
}

type containerWithImageConfigMetadataArgs struct {
	Healthcheck string `default:""`
	OnBuild     dagql.Optional[dagql.ArrayInput[dagql.String]]
	Shell       dagql.Optional[dagql.ArrayInput[dagql.String]]
	Volumes     dagql.Optional[dagql.ArrayInput[dagql.String]]
	StopSignal  string `default:""`
}

func (s *containerSchema) withImageConfigMetadata(ctx context.Context, parent *core.Container, args containerWithImageConfigMetadataArgs) (*core.Container, error) {
	var healthcheck *dockerspec.HealthcheckConfig
	if args.Healthcheck != "" {
		healthcheck = new(dockerspec.HealthcheckConfig)
		if err := json.Unmarshal([]byte(args.Healthcheck), healthcheck); err != nil {
			return nil, fmt.Errorf("failed to decode healthcheck metadata: %w", err)
		}
	}

	return parent.UpdateImageConfig(ctx, func(cfg dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig {
		if args.Healthcheck != "" {
			cfg.Healthcheck = healthcheck
		}
		if args.OnBuild.Valid {
			onBuild := make([]string, 0, len(args.OnBuild.Value))
			for _, trigger := range args.OnBuild.Value {
				onBuild = append(onBuild, trigger.String())
			}
			cfg.OnBuild = onBuild
		}
		if args.Shell.Valid {
			shellArgs := make([]string, 0, len(args.Shell.Value))
			for _, shellArg := range args.Shell.Value {
				shellArgs = append(shellArgs, shellArg.String())
			}
			cfg.Shell = shellArgs
		}
		if args.Volumes.Valid {
			volumes := make(map[string]struct{}, len(args.Volumes.Value))
			for _, volumePath := range args.Volumes.Value {
				volumes[volumePath.String()] = struct{}{}
			}
			cfg.Volumes = volumes
		}
		if args.StopSignal != "" {
			cfg.StopSignal = args.StopSignal
		}
		return cfg
	})
}

type containerWithoutVariableArgs struct {
	Name string
}

func (s *containerSchema) withoutEnvVariable(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithoutVariableArgs) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.UpdateImageConfig(ctx, func(cfg dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig {
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

func (s *containerSchema) envVariables(ctx context.Context, parent *core.Container, args struct{}) (dagql.Array[core.EnvVariable], error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	vars := make([]core.EnvVariable, 0, len(cfg.Env))

	core.WalkEnv(cfg.Env, func(k, v, _ string) {
		vars = append(vars, core.EnvVariable{Name: k, Value: v})
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

func (s *containerSchema) labels(ctx context.Context, parent *core.Container, args struct{}) (dagql.Array[Label], error) {
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
	Path     string
	Source   core.DirectoryID
	Owner    string `default:""`
	ReadOnly bool   `default:"false"`
	Expand   bool   `default:"false"`
}

func (s *containerSchema) withMountedDirectory(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithMountedDirectoryArgs) (*core.Container, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}
	if ownerNeedsLookup(args.Owner) {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return nil, err
		}
		if err := cache.Evaluate(ctx, parent); err != nil {
			return nil, err
		}
	}

	dir, err := args.Source.Load(ctx, srv)
	if err != nil {
		return nil, err
	}

	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return nil, err
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithMountedDirectory(ctx, parent, path, dir, args.Owner, args.ReadOnly)
}

type containerWithAnnotationArgs struct {
	Name  string
	Value string
}

func (s *containerSchema) withAnnotation(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithAnnotationArgs) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithAnnotation(ctx, args.Name, args.Value)
}

type containerWithoutAnnotationArgs struct {
	Name string
}

func (s *containerSchema) withoutAnnotation(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithoutAnnotationArgs) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithoutAnnotation(ctx, args.Name)
}

func (s *containerSchema) exists(ctx context.Context, parent dagql.ObjectResult[*core.Container], args existsArgs) (dagql.Boolean, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get server: %w", err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return false, err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return false, err
	}
	exists, err := parent.Self().Exists(ctx, parent, srv, args.Path, args.ExpectedType.Value, args.DoNotFollowSymlinks)
	return dagql.NewBoolean(exists), err
}

func (s *containerSchema) stat(ctx context.Context, parent dagql.ObjectResult[*core.Container], args statArgs) (*core.Stat, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return nil, err
	}
	return parent.Self().Stat(ctx, parent, srv, args.Path, args.DoNotFollowSymlinks)
}

type containerPublishArgs struct {
	Address           dagql.String
	PlatformVariants  []core.ContainerID `default:"[]"`
	ForcedCompression dagql.Optional[core.ImageLayerCompression]
	MediaTypes        core.ImageMediaTypes `default:"OCI"`
}

func (s *containerSchema) publish(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerPublishArgs) (dagql.String, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get server: %w", err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return "", err
	}
	variantResults, err := dagql.LoadIDResults(ctx, srv, args.PlatformVariants)
	if err != nil {
		return "", err
	}
	evals := make([]dagql.AnyResult, 0, 1+len(variantResults))
	evals = append(evals, parent)
	for _, variant := range variantResults {
		if variant.Self() != nil {
			evals = append(evals, variant)
		}
	}
	if err := cache.Evaluate(ctx, evals...); err != nil {
		return "", err
	}
	variants := make([]*core.Container, 0, len(variantResults))
	for _, variant := range variantResults {
		if variant.Self() != nil {
			variants = append(variants, variant.Self())
		}
	}
	ref, err := parent.Self().Publish(
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
	Expand bool   `default:"false"`
}

func (s *containerSchema) withMountedFile(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithMountedFileArgs) (*core.Container, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}
	if ownerNeedsLookup(args.Owner) {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return nil, err
		}
		if err := cache.Evaluate(ctx, parent); err != nil {
			return nil, err
		}
	}

	file, err := args.Source.Load(ctx, srv)
	if err != nil {
		return nil, err
	}

	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return nil, err
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithMountedFile(ctx, parent, path, file, args.Owner, false)
}

type containerWithMountedCacheArgs struct {
	Path    string
	Cache   core.CacheVolumeID
	Source  dagql.Optional[core.DirectoryID]
	Sharing core.CacheSharingMode `default:"SHARED"`
	Owner   string                `default:""`
	Expand  bool                  `default:"false"`
}

func (s *containerSchema) withMountedCacheDynamicInputs(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Container],
	args containerWithMountedCacheArgs,
	req *dagql.CallRequest,
) error {
	hasSourceArg := req.HasArg("source")
	hasSharingArg := req.HasArg("sharing")
	hasOwnerArg := req.HasArg("owner")
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return err
	}

	cache, err := args.Cache.Load(ctx, srv)
	if err != nil {
		return err
	}
	if cache.Self() == nil {
		return errors.New("cache volume is nil")
	}

	cacheSelf := cache.Self()
	sharing := cacheSelf.Sharing
	if sharing == "" {
		sharing = core.CacheSharingModeShared
	}
	needsRewrite := hasSourceArg || hasSharingArg || hasOwnerArg
	if hasSharingArg {
		sharing = args.Sharing
	}

	owner := cacheSelf.Owner
	if hasOwnerArg {
		owner = args.Owner
	}
	if ownerNeedsLookup(owner) {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := cache.Evaluate(ctx, parent); err != nil {
			return err
		}
		owner, err = parent.Self().ResolveOwnership(ctx, parent, owner)
		if err != nil {
			return fmt.Errorf("failed to resolve ownership for %s: %w", owner, err)
		}
		needsRewrite = true
	}

	source := cacheSelf.Source
	if hasSourceArg {
		source = args.Source
	}
	if !needsRewrite {
		return nil
	}

	cacheSelectArgs := []dagql.NamedInput{
		{Name: "key", Value: dagql.NewString(cacheSelf.Key)},
		{Name: "namespace", Value: dagql.NewString(cacheSelf.Namespace)},
		{Name: "sharing", Value: sharing},
		{Name: "owner", Value: dagql.NewString(owner)},
	}
	if source.Valid {
		sourceID, err := source.Value.ID()
		if err != nil {
			return fmt.Errorf("resolve cache source ID: %w", err)
		}
		cacheSelectArgs = append(cacheSelectArgs, dagql.NamedInput{
			Name:  "source",
			Value: dagql.Opt(dagql.NewID[*core.Directory](sourceID)),
		})
	}

	var resolvedCache dagql.Result[*core.CacheVolume]
	if err := srv.Select(ctx, srv.Root(), &resolvedCache, dagql.Selector{
		Field: "cacheVolume",
		Args:  cacheSelectArgs,
	}); err != nil {
		return err
	}
	resolvedCacheID, err := resolvedCache.ID()
	if err != nil {
		return fmt.Errorf("resolve rewritten cache volume ID: %w", err)
	}
	args.Cache = dagql.NewID[*core.CacheVolume](resolvedCacheID)
	return req.SetArgInput(ctx, "cache", args.Cache, false)
}

func (s *containerSchema) withMountedCache(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithMountedCacheArgs) (*core.Container, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	cache, err := args.Cache.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	if cache.Self() == nil {
		return nil, errors.New("cache volume is nil")
	}

	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return nil, err
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithMountedCache(ctx, path, cache)
}

type containerWithMountedTempArgs struct {
	Path   string
	Size   dagql.Optional[dagql.Int]
	Expand bool `default:"false"`
}

func (s *containerSchema) withMountedTemp(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithMountedTempArgs) (*core.Container, error) {
	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return nil, err
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithMountedTemp(ctx, path, args.Size.Value.Int())
}

type containerWithoutMountArgs struct {
	Path   string
	Expand bool `default:"false"`
}

func (s *containerSchema) withoutMount(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithoutMountArgs) (*core.Container, error) {
	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return nil, err
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithoutMount(ctx, path)
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

func (s *containerSchema) withLabel(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithLabelArgs) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.UpdateImageConfig(ctx, func(cfg dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig {
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

func (s *containerSchema) withoutLabel(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithoutLabelArgs) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.UpdateImageConfig(ctx, func(cfg dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig {
		delete(cfg.Labels, args.Name)
		return cfg
	})
}

type WithHealthcheckArgs struct {
	Args          []string
	Shell         dagql.Optional[dagql.Boolean]
	Timeout       dagql.Optional[dagql.String]
	Interval      dagql.Optional[dagql.String]
	StartPeriod   dagql.Optional[dagql.String]
	StartInterval dagql.Optional[dagql.String]
	Retries       dagql.Optional[dagql.Int]
}

func (s *containerSchema) withHealthcheck(ctx context.Context, parent *core.Container, args WithHealthcheckArgs) (*core.Container, error) {
	var healthcheck dockerspec.HealthcheckConfig
	if args.Shell.Valid && args.Shell.Value.Bool() {
		if len(args.Args) != 1 {
			return nil, fmt.Errorf("WithHealthcheck args must be a single element when shell mode is set")
		}
		healthcheck.Test = append([]string{"CMD-SHELL"}, args.Args...)
	} else {
		if len(args.Args) == 0 {
			return nil, fmt.Errorf("WithHealthcheck args is missing")
		}
		healthcheck.Test = append([]string{"CMD"}, args.Args...)
	}
	if args.Timeout.Valid {
		timeout, err := time.ParseDuration(args.Timeout.Value.String())
		if err != nil {
			return nil, err
		}
		healthcheck.Timeout = timeout
	}
	if args.Interval.Valid {
		interval, err := time.ParseDuration(args.Interval.Value.String())
		if err != nil {
			return nil, err
		}
		healthcheck.Interval = interval
	}
	if args.StartPeriod.Valid {
		startPeriod, err := time.ParseDuration(args.StartPeriod.Value.String())
		if err != nil {
			return nil, err
		}
		healthcheck.StartPeriod = startPeriod
	}
	if args.StartInterval.Valid {
		startInterval, err := time.ParseDuration(args.StartInterval.Value.String())
		if err != nil {
			return nil, err
		}
		healthcheck.StartInterval = startInterval
	}
	if args.Retries.Valid {
		healthcheck.Retries = int(args.Retries.Value)
	}
	return parent.UpdateImageConfig(ctx, func(cfg dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig {
		cfg.Healthcheck = &healthcheck
		return cfg
	})
}

func (s *containerSchema) withoutHealthcheck(ctx context.Context, parent *core.Container, args struct{}) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig {
		cfg.Healthcheck = nil
		return cfg
	})
}

type HealthcheckConfig struct {
	Args          []string `field:"true" doc:"Healthcheck command arguments."`
	Shell         bool     `field:"true" doc:"Healthcheck command is a shell command."`
	Timeout       string   `field:"true" doc:"Healthcheck timeout. Example:3s"`
	Interval      string   `field:"true" doc:"Interval between running healthcheck. Example:30s"`
	StartPeriod   string   `field:"true" doc:"StartPeriod allows for failures during this initial startup period which do not count towards maximum number of retries. Example:0s"`
	StartInterval string   `field:"true" doc:"StartInterval configures the duration between checks during the startup phase. Example:5s"`
	Retries       int      `field:"true" doc:"The maximum number of consecutive failures before the container is marked as unhealthy. Example:3"`
}

func (HealthcheckConfig) Type() *ast.Type {
	return &ast.Type{
		NamedType: "HealthcheckConfig",
		NonNull:   true,
	}
}

func (HealthcheckConfig) TypeDescription() string {
	return "Image healthcheck configuration."
}

func (s *containerSchema) healthcheck(ctx context.Context, parent *core.Container, args struct{}) (inst dagql.Nullable[HealthcheckConfig], err error) {
	if parent.Config.Healthcheck == nil || len(parent.Config.Healthcheck.Test) == 0 || parent.Config.Healthcheck.Test[0] == "NONE" {
		return dagql.Null[HealthcheckConfig](), nil
	}

	hcc := HealthcheckConfig{
		Timeout:       parent.Config.Healthcheck.Timeout.String(),
		Interval:      parent.Config.Healthcheck.Interval.String(),
		StartPeriod:   parent.Config.Healthcheck.StartPeriod.String(),
		StartInterval: parent.Config.Healthcheck.StartInterval.String(),
		Retries:       parent.Config.Healthcheck.Retries,
	}

	testType := parent.Config.Healthcheck.Test[0]
	switch testType {
	case "CMD-SHELL":
		if len(parent.Config.Healthcheck.Test) != 2 {
			return inst, fmt.Errorf("HEALTHCHECK command is a shell command, but got unexpected length of %d; cmd=%v",
				len(parent.Config.Healthcheck.Test), parent.Config.Healthcheck.Test)
		}
		hcc.Shell = true
	case "CMD":
		if len(parent.Config.Healthcheck.Test) < 2 {
			return inst, fmt.Errorf("HEALTHCHECK command is empty")
		}
	default:
		return inst, fmt.Errorf("unexpected HEALTHCHECK command of type %s found", testType)
	}
	hcc.Args = parent.Config.Healthcheck.Test[1:]
	return dagql.NonNull(hcc), nil
}

type containerDirectoryArgs struct {
	Path   string
	Expand bool `default:"false"`
}

func (s *containerSchema) directory(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerDirectoryArgs) (dagql.ObjectResult[*core.Directory], error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}

	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}

	return parent.Self().Directory(ctx, parent, path)
}

type containerFileArgs struct {
	Path   string
	Expand bool `default:"false"`
}

func (s *containerSchema) file(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerFileArgs) (dagql.ObjectResult[*core.File], error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.File]{}, err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return dagql.ObjectResult[*core.File]{}, err
	}

	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return dagql.ObjectResult[*core.File]{}, err
	}

	return parent.Self().File(ctx, parent, path)
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

func ownerNeedsLookup(owner string) bool {
	if owner == "" {
		return false
	}

	uidOrName, gidOrName, hasGroup := strings.Cut(owner, ":")
	if _, err := strconv.Atoi(uidOrName); err != nil {
		return true
	}
	if hasGroup {
		if _, err := strconv.Atoi(gidOrName); err != nil {
			return true
		}
	}
	return false
}

func expandEnvVar(ctx context.Context, parent *core.Container, input string, expand bool) (string, error) {
	if !expand {
		return input, nil
	}

	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return "", err
	}

	secretEnvs := []string{}
	for _, secret := range parent.Secrets {
		secretEnvs = append(secretEnvs, secret.EnvName)
	}

	var secretEnvFoundError error
	expanded := os.Expand(input, func(k string) string {
		// set error if its a secret env variable
		if slices.Contains(secretEnvs, k) {
			secretEnvFoundError = fmt.Errorf("expand cannot be used with secret env variable %q", k)
			return ""
		}

		v, _ := core.LookupEnv(cfg.Env, k)
		return v
	})

	if secretEnvFoundError != nil {
		return "", secretEnvFoundError
	}

	return expanded, nil
}

type containerWithSecretVariableArgs struct {
	Name   string
	Secret core.SecretID
}

func (s *containerSchema) withSecretVariable(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithSecretVariableArgs) (*core.Container, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	secret, err := args.Secret.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithSecretVariable(ctx, args.Name, secret)
}

type containerWithoutSecretVariableArgs struct {
	Name string
}

func (s *containerSchema) withoutSecretVariable(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithoutSecretVariableArgs) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithoutSecretVariable(ctx, args.Name)
}

type containerWithMountedSecretArgs struct {
	Path   string
	Source core.SecretID
	Owner  string `default:""`
	Mode   int    `default:"0400"` // FIXME(vito): verify octal
	Expand bool   `default:"false"`
}

func (s *containerSchema) withMountedSecret(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithMountedSecretArgs) (*core.Container, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}
	if ownerNeedsLookup(args.Owner) {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return nil, err
		}
		if err := cache.Evaluate(ctx, parent); err != nil {
			return nil, err
		}
	}

	secret, err := args.Source.Load(ctx, srv)
	if err != nil {
		return nil, err
	}

	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return nil, err
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithMountedSecret(ctx, parent, path, secret, args.Owner, fs.FileMode(args.Mode))
}

type containerWithDirectoryArgs struct {
	WithDirectoryArgs
	Owner  string `default:""`
	Expand bool   `default:"false"`
}

func (s *containerSchema) withDirectory(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithDirectoryArgs) (*core.Container, error) {
	if ownerNeedsLookup(args.Owner) {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return nil, err
		}
		if err := cache.Evaluate(ctx, parent); err != nil {
			return nil, err
		}
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	dir, err := cmp.Or(args.Source, args.Directory).Load(ctx, srv)
	if err != nil {
		return nil, err
	}

	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return nil, err
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithDirectory(
		ctx,
		parent,
		path,
		dir,
		args.CopyFilter,
		args.Owner,
	)
}

type containerWithFileArgs struct {
	WithFileArgs
	Owner  string `default:""`
	Expand bool   `default:"false"`
}

func (s *containerSchema) withFile(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithFileArgs) (inst dagql.ObjectResult[*core.Container], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get server: %w", err)
	}
	if ownerNeedsLookup(args.Owner) {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return inst, err
		}
		if err := cache.Evaluate(ctx, parent); err != nil {
			return inst, err
		}
	}

	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return inst, err
	}

	var perms *int
	if args.Permissions.Valid {
		p := int(args.Permissions.Value)
		perms = &p
	}

	file, err := args.Source.Load(ctx, srv)
	if err != nil {
		return inst, err
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return inst, err
	}
	ctr, err = ctr.WithFile(
		ctx,
		parent,
		srv,
		path,
		file,
		perms,
		args.Owner,
	)
	if err != nil {
		return inst, err
	}

	inst, err = dagql.NewObjectResultForCurrentCall(ctx, srv, ctr)
	return inst, err
}

type containerWithFilesArgs struct {
	WithFilesArgs
	Owner  string `default:""`
	Expand bool   `default:"false"`
}

func (s *containerSchema) withFiles(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithFilesArgs) (inst dagql.ObjectResult[*core.Container], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get server: %w", err)
	}
	if ownerNeedsLookup(args.Owner) {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return inst, err
		}
		if err := cache.Evaluate(ctx, parent); err != nil {
			return inst, err
		}
	}

	files := []dagql.ObjectResult[*core.File]{}
	for _, id := range args.Sources {
		file, err := id.Load(ctx, srv)
		if err != nil {
			return inst, err
		}
		files = append(files, file)
	}

	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return inst, err
	}

	var perms *int
	if args.Permissions.Valid {
		p := int(args.Permissions.Value)
		perms = &p
	}
	current := parent
	for _, file := range files {
		fileID, err := file.ID()
		if err != nil {
			return inst, err
		}
		filePath := filepath.Join(path, filepath.Base(file.Self().File))
		selectArgs := []dagql.NamedInput{
			{Name: "path", Value: dagql.String(filePath)},
			{Name: "source", Value: dagql.NewID[*core.File](fileID)},
		}
		if perms != nil {
			selectArgs = append(selectArgs, dagql.NamedInput{Name: "permissions", Value: dagql.Opt(dagql.Int(*perms))})
		}
		if args.Owner != "" {
			selectArgs = append(selectArgs, dagql.NamedInput{Name: "owner", Value: dagql.String(args.Owner)})
		}
		var next dagql.ObjectResult[*core.Container]
		if err := srv.Select(ctx, current, &next, dagql.Selector{
			Field: "withFile",
			Args:  selectArgs,
		}); err != nil {
			return inst, err
		}
		current = next
	}
	return current, nil
}

type containerWithoutDirectoryArgs struct {
	Path   string
	Expand bool `default:"false"`
}

func (s *containerSchema) withoutDirectory(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithoutDirectoryArgs) (inst dagql.ObjectResult[*core.Container], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get server: %w", err)
	}

	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return inst, err
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return inst, err
	}
	ctr, err = ctr.WithoutPaths(ctx, parent, srv, path)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ctr)
}

type containerWithoutFileArgs struct {
	Path   string
	Expand bool `default:"false"`
}

func (s *containerSchema) withoutFile(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithoutFileArgs) (inst dagql.ObjectResult[*core.Container], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get server: %w", err)
	}

	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return inst, err
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return inst, err
	}
	ctr, err = ctr.WithoutPaths(ctx, parent, srv, path)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ctr)
}

type containerWithoutFilesArgs struct {
	Paths  []string
	Expand bool `default:"false"`
}

func (s *containerSchema) withoutFiles(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithoutFilesArgs) (inst dagql.ObjectResult[*core.Container], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get server: %w", err)
	}

	paths := args.Paths
	for i, p := range args.Paths {
		paths[i], err = expandEnvVar(ctx, parent.Self(), p, args.Expand)
		if err != nil {
			return inst, err
		}
	}

	current := parent
	for _, path := range paths {
		var next dagql.ObjectResult[*core.Container]
		if err := srv.Select(ctx, current, &next, dagql.Selector{
			Field: "withoutFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(path)},
			},
		}); err != nil {
			return inst, err
		}
		current = next
	}
	return current, nil
}

type containerWithNewFileArgs struct {
	Path        string
	Contents    string
	Permissions int    `default:"0644"`
	Owner       string `default:""`
	Expand      bool   `default:"false"`
}

func (s *containerSchema) withNewFile(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithNewFileArgs) (inst dagql.ObjectResult[*core.Container], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get server: %w", err)
	}
	if ownerNeedsLookup(args.Owner) {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return inst, err
		}
		if err := cache.Evaluate(ctx, parent); err != nil {
			return inst, err
		}
	}

	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return inst, err
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return inst, err
	}
	ctr, err = ctr.WithNewFile(ctx, parent, path, []byte(args.Contents), fs.FileMode(args.Permissions), args.Owner)
	if err != nil {
		return inst, err
	}

	return dagql.NewObjectResultForCurrentCall(ctx, srv, ctr)
}

type containerWithNewFileArgsLegacy struct {
	Path        string
	Contents    string `default:""`
	Permissions int    `default:"0644"`
	Owner       string `default:""`
}

func (s *containerSchema) withNewFileLegacy(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithNewFileArgsLegacy) (inst dagql.ObjectResult[*core.Container], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get server: %w", err)
	}
	if ownerNeedsLookup(args.Owner) {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return inst, err
		}
		if err := cache.Evaluate(ctx, parent); err != nil {
			return inst, err
		}
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return inst, err
	}
	ctr, err = ctr.WithNewFile(ctx, parent, args.Path, []byte(args.Contents), fs.FileMode(args.Permissions), args.Owner)
	if err != nil {
		return inst, err
	}

	return dagql.NewObjectResultForCurrentCall(ctx, srv, ctr)
}

type containerWithUnixSocketArgs struct {
	Path   string
	Source core.SocketID
	Owner  string `default:""`
	Expand bool   `default:"false"`
}

func (s *containerSchema) withUnixSocket(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithUnixSocketArgs) (*core.Container, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}
	if ownerNeedsLookup(args.Owner) {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return nil, err
		}
		if err := cache.Evaluate(ctx, parent); err != nil {
			return nil, err
		}
	}

	socket, err := args.Source.Load(ctx, srv)
	if err != nil {
		return nil, err
	}

	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return nil, err
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithUnixSocketFromParent(ctx, parent, path, socket, args.Owner)
}

type containerWithoutUnixSocketArgs struct {
	Path   string
	Expand bool `default:"false"`
}

func (s *containerSchema) withoutUnixSocket(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithoutUnixSocketArgs) (*core.Container, error) {
	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return nil, err
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithoutUnixSocket(ctx, path)
}

func (s *containerSchema) platform(ctx context.Context, parent *core.Container, args struct{}) (core.Platform, error) {
	return parent.Platform, nil
}

type containerExportArgs struct {
	Path              string
	PlatformVariants  []core.ContainerID `default:"[]"`
	ForcedCompression dagql.Optional[core.ImageLayerCompression]
	MediaTypes        core.ImageMediaTypes `default:"OCI"`
	Expand            bool                 `default:"false"`
}

func (s *containerSchema) export(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerExportArgs) (dagql.String, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	srv, err := query.Server.Server(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get server: %w", err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return "", err
	}
	variantResults, err := dagql.LoadIDResults(ctx, srv, args.PlatformVariants)
	if err != nil {
		return "", err
	}
	evals := make([]dagql.AnyResult, 0, 1+len(variantResults))
	evals = append(evals, parent)
	for _, variant := range variantResults {
		if variant.Self() != nil {
			evals = append(evals, variant)
		}
	}
	if err := cache.Evaluate(ctx, evals...); err != nil {
		return "", err
	}
	variants := make([]*core.Container, 0, len(variantResults))
	for _, variant := range variantResults {
		if variant.Self() != nil {
			variants = append(variants, variant.Self())
		}
	}

	path, err := expandEnvVar(ctx, parent.Self(), args.Path, args.Expand)
	if err != nil {
		return "", err
	}

	_, err = parent.Self().Export(
		ctx,
		core.ExportOpts{
			Dest:              path,
			PlatformVariants:  variants,
			ForcedCompression: args.ForcedCompression.Value,
			MediaTypes:        args.MediaTypes,
			Tar:               true,
		},
	)
	if err != nil {
		return "", err
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get engine client: %w", err)
	}
	stat, err := bk.StatCallerHostPath(ctx, path, true)
	if err != nil {
		return "", err
	}
	return dagql.String(stat.Path), err
}

func (s *containerSchema) exportLegacy(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerExportArgs) (dagql.Boolean, error) {
	_, err := s.export(ctx, parent, args)
	if err != nil {
		return false, err
	}
	return true, nil
}

type containerAsTarballArgs struct {
	PlatformVariants  []core.ContainerID `default:"[]"`
	ForcedCompression dagql.Optional[core.ImageLayerCompression]
	MediaTypes        core.ImageMediaTypes `default:"OCI"`
}

func (s *containerSchema) asTarball(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Container],
	args containerAsTarballArgs,
) (inst dagql.ObjectResult[*core.File], rerr error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	srv, err := query.Server.Server(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get server: %w", err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return inst, err
	}
	platformVariantResults, err := dagql.LoadIDResults(ctx, srv, args.PlatformVariants)
	if err != nil {
		return inst, err
	}
	evals := make([]dagql.AnyResult, 0, 1+len(platformVariantResults))
	evals = append(evals, parent)
	for _, variant := range platformVariantResults {
		if variant.Self() != nil {
			evals = append(evals, variant)
		}
	}
	if err := cache.Evaluate(ctx, evals...); err != nil {
		return inst, err
	}
	platformVariants := make([]*core.Container, 0, len(platformVariantResults))
	for _, variant := range platformVariantResults {
		if variant.Self() != nil {
			platformVariants = append(platformVariants, variant.Self())
		}
	}

	f, err := parent.Self().AsTarball(ctx, platformVariants,
		args.ForcedCompression.Value,
		args.MediaTypes,
		"container.tar",
	)
	if err != nil {
		return inst, err
	}
	fileInst, err := dagql.NewObjectResultForCurrentCall(ctx, srv, f)
	if err != nil {
		return inst, err
	}
	return fileInst, nil
}

type containerExportImageArgs struct {
	Name string

	PlatformVariants  []core.ContainerID `default:"[]"`
	ForcedCompression dagql.Optional[core.ImageLayerCompression]
	MediaTypes        core.ImageMediaTypes `default:"OCI"`
}

func (s *containerSchema) exportImage(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Container],
	args containerExportImageArgs,
) (_ core.Void, rerr error) {
	refName, err := reference.ParseNormalizedNamed(args.Name)
	if err != nil {
		return core.Void{}, fmt.Errorf("failed to parse image address %s: %w", args.Name, err)
	}
	refName = reference.TagNameOnly(refName)

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return core.Void{}, err
	}
	srv, err := query.Server.Server(ctx)
	if err != nil {
		return core.Void{}, fmt.Errorf("failed to get server: %w", err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return core.Void{}, err
	}
	platformVariantResults, err := dagql.LoadIDResults(ctx, srv, args.PlatformVariants)
	if err != nil {
		return core.Void{}, err
	}
	evals := make([]dagql.AnyResult, 0, 1+len(platformVariantResults))
	evals = append(evals, parent)
	for _, variant := range platformVariantResults {
		if variant.Self() != nil {
			evals = append(evals, variant)
		}
	}
	if err := cache.Evaluate(ctx, evals...); err != nil {
		return core.Void{}, err
	}
	platformVariants := make([]*core.Container, 0, len(platformVariantResults))
	for _, variant := range platformVariantResults {
		if variant.Self() != nil {
			platformVariants = append(platformVariants, variant.Self())
		}
	}

	_, err = parent.Self().Export(ctx, core.ExportOpts{
		Dest:              refName.String(),
		PlatformVariants:  platformVariants,
		ForcedCompression: args.ForcedCompression.Value,
		MediaTypes:        args.MediaTypes,
		Tar:               false,
	})
	if err != nil {
		return core.Void{}, err
	}
	return core.Void{}, nil
}

type containerImportArgs struct {
	Source core.FileID
	Tag    string `default:""`
}

func (s *containerSchema) import_(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerImportArgs) (*core.Container, error) {
	start := time.Now()
	slog.ExtraDebug("importing container", "source", args.Source.Display(), "tag", args.Tag)
	defer func() {
		slog.ExtraDebug("done importing container", "source", args.Source.Display(), "tag", args.Tag, "took", start)
	}()
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	source, err := args.Source.Load(ctx, srv)
	if err != nil {
		return nil, err
	}

	r, err := source.Self().Open(ctx)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.Import(ctx, r, args.Tag)
}

type containerWithRegistryAuthArgs struct {
	Address  string
	Username string
	Secret   core.SecretID
}

func (s *containerSchema) withRegistryAuth(ctx context.Context, parent *core.Container, args containerWithRegistryAuthArgs) (*core.Container, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	srv, err := query.Server.Server(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	secret, err := args.Secret.Load(ctx, srv)
	if err != nil {
		return nil, err
	}

	secretBytes, err := secret.Self().Plaintext(ctx)
	if err != nil {
		return nil, err
	}

	auth, err := query.Auth(ctx)
	if err != nil {
		return nil, err
	}
	if err := auth.AddCredential(args.Address, args.Username, string(secretBytes)); err != nil {
		return nil, err
	}

	return parent, nil
}

type containerWithoutRegistryAuthArgs struct {
	Address string
}

func (s *containerSchema) withoutRegistryAuth(ctx context.Context, parent *core.Container, args containerWithoutRegistryAuthArgs) (*core.Container, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	auth, err := query.Auth(ctx)
	if err != nil {
		return nil, err
	}
	if err := auth.RemoveCredential(args.Address); err != nil {
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

func (s *containerSchema) withServiceBinding(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithServiceBindingArgs) (*core.Container, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	svc, err := args.Service.Load(ctx, srv)
	if err != nil {
		return nil, err
	}

	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithServiceBinding(ctx, svc, args.Alias)
}

type containerWithExposedPortArgs struct {
	Port                        int
	Protocol                    core.NetworkProtocol `default:"TCP"`
	Description                 *string
	ExperimentalSkipHealthcheck bool `default:"false"`
}

func (s *containerSchema) withExposedPort(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithExposedPortArgs) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithExposedPort(core.Port{
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

func (s *containerSchema) withoutExposedPort(ctx context.Context, parent dagql.ObjectResult[*core.Container], args containerWithoutExposedPortArgs) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	return ctr.WithoutExposedPort(args.Port, args.Protocol)
}

func (s *containerSchema) exposedPorts(ctx context.Context, parent *core.Container, args struct{}) (dagql.Array[core.Port], error) {
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
			var err error
			p, err = core.NewPortFromOCI(ociPort)
			if err != nil {
				// ignore errors when parsing from OCI
				continue
			}
		}
		exposedPorts = append(exposedPorts, p)
	}

	return exposedPorts, nil
}

func (s *containerSchema) withFocus(ctx context.Context, parent *core.Container, args struct{}) (*core.Container, error) {
	return parent, nil
}

func (s *containerSchema) withoutFocus(ctx context.Context, parent *core.Container, args struct{}) (*core.Container, error) {
	return parent, nil
}

type containerWithDefaultTerminalCmdArgs struct {
	core.DefaultTerminalCmdOpts
}

func (s *containerSchema) withDefaultTerminalCmd(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Container],
	args containerWithDefaultTerminalCmdArgs,
) (*core.Container, error) {
	ctr, err := core.NewContainerChild(ctx, parent)
	if err != nil {
		return nil, err
	}
	ctr.DefaultTerminalCmd = args.DefaultTerminalCmdOpts
	return ctr, nil
}

type containerTerminalArgs struct {
	core.TerminalArgs
}

func (s *containerSchema) terminal(
	ctx context.Context,
	ctr dagql.ObjectResult[*core.Container],
	args containerTerminalArgs,
) (res dagql.ObjectResult[*core.Container], _ error) {
	if len(args.Cmd) == 0 {
		args.Cmd = ctr.Self().DefaultTerminalCmd.Args
	}

	if !args.ExperimentalPrivilegedNesting.Valid {
		args.ExperimentalPrivilegedNesting = ctr.Self().DefaultTerminalCmd.ExperimentalPrivilegedNesting
	}

	if !args.InsecureRootCapabilities.Valid {
		args.InsecureRootCapabilities = ctr.Self().DefaultTerminalCmd.InsecureRootCapabilities
	}

	// if still no args, default to sh
	if len(args.Cmd) == 0 {
		args.Cmd = []string{"sh"}
	}

	ctrDig, err := ctr.ContentPreferredDigest(ctx)
	if err != nil {
		return res, err
	}
	ctrID, err := ctr.ID()
	if err != nil {
		return res, err
	}
	err = ctr.Self().Terminal(ctx, ctrID, ctrDig, ctr, &args.TerminalArgs)
	if err != nil {
		return res, err
	}

	return ctr, nil
}

func (s *containerSchema) terminalLegacy(
	ctx context.Context,
	ctr dagql.ObjectResult[*core.Container],
	args containerTerminalArgs,
) (*core.TerminalLegacy, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	// HACK: when attempting to construct a legacy terminal, just spin up a new
	// terminal attachable. The returned terminal is definitely invalid, but,
	// the intention was probably to debug it anyways, so we're probably okay.
	var inputs []dagql.NamedInput
	if args.Cmd != nil {
		inputs = append(inputs, dagql.NamedInput{
			Name:  "cmd",
			Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(args.Cmd...)),
		})
	}
	if args.ExperimentalPrivilegedNesting.Valid {
		inputs = append(inputs, dagql.NamedInput{
			Name:  "experimentalPrivilegedNesting",
			Value: args.ExperimentalPrivilegedNesting,
		})
	}
	if args.InsecureRootCapabilities.Valid {
		inputs = append(inputs, dagql.NamedInput{
			Name:  "insecureRootCapabilities",
			Value: args.InsecureRootCapabilities,
		})
	}

	err = srv.Select(ctx, ctr, new(dagql.Result[*core.Container]),
		dagql.Selector{
			Field: "terminal",
			Args:  inputs,
		},
	)
	if err != nil {
		return nil, err
	}

	return &core.TerminalLegacy{}, nil
}

func (s *containerSchema) terminalLegacyWebsocketEndpoint(ctx context.Context, parent *core.TerminalLegacy, args struct{}) (string, error) {
	return "", nil
}
