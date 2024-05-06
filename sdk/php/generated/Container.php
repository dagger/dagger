<?php

/**
 * This class has been generated by dagger-php-sdk. DO NOT EDIT.
 */

declare(strict_types=1);

namespace Dagger;

/**
 * An OCI-compatible container, also known as a Docker container.
 */
class Container extends Client\AbstractObject implements Client\IdAble
{
    /**
     * Turn the container into a Service.
     *
     * Be sure to set any exposed ports before this conversion.
     */
    public function asService(): Service
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('asService');
        return new \Dagger\Service($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Returns a File representing the container serialized to a tarball.
     */
    public function asTarball(
        ?array $platformVariants = null,
        ?ImageLayerCompression $forcedCompression = null,
        ?ImageMediaTypes $mediaTypes = null,
    ): File
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('asTarball');
        if (null !== $platformVariants) {
        $innerQueryBuilder->setArgument('platformVariants', $platformVariants);
        }
        if (null !== $forcedCompression) {
        $innerQueryBuilder->setArgument('forcedCompression', $forcedCompression);
        }
        if (null !== $mediaTypes) {
        $innerQueryBuilder->setArgument('mediaTypes', $mediaTypes);
        }
        return new \Dagger\File($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Initializes this container from a Dockerfile build.
     */
    public function build(
        DirectoryId|Directory $context,
        ?string $dockerfile = 'Dockerfile',
        ?string $target = '',
        ?array $buildArgs = null,
        ?array $secrets = null,
    ): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('build');
        $innerQueryBuilder->setArgument('context', $context);
        if (null !== $dockerfile) {
        $innerQueryBuilder->setArgument('dockerfile', $dockerfile);
        }
        if (null !== $target) {
        $innerQueryBuilder->setArgument('target', $target);
        }
        if (null !== $buildArgs) {
        $innerQueryBuilder->setArgument('buildArgs', $buildArgs);
        }
        if (null !== $secrets) {
        $innerQueryBuilder->setArgument('secrets', $secrets);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves default arguments for future commands.
     */
    public function defaultArgs(): array
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('defaultArgs');
        return (array)$this->queryLeaf($leafQueryBuilder, 'defaultArgs');
    }

    /**
     * Retrieves a directory at the given path.
     *
     * Mounts are included.
     */
    public function directory(string $path): Directory
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('directory');
        $innerQueryBuilder->setArgument('path', $path);
        return new \Dagger\Directory($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves entrypoint to be prepended to the arguments of all commands.
     */
    public function entrypoint(): array
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('entrypoint');
        return (array)$this->queryLeaf($leafQueryBuilder, 'entrypoint');
    }

    /**
     * Retrieves the value of the specified environment variable.
     */
    public function envVariable(string $name): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('envVariable');
        $leafQueryBuilder->setArgument('name', $name);
        return (string)$this->queryLeaf($leafQueryBuilder, 'envVariable');
    }

    /**
     * Retrieves the list of environment variables passed to commands.
     */
    public function envVariables(): array
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('envVariables');
        return (array)$this->queryLeaf($leafQueryBuilder, 'envVariables');
    }

    /**
     * EXPERIMENTAL API! Subject to change/removal at any time.
     *
     * Configures all available GPUs on the host to be accessible to this container.
     *
     * This currently works for Nvidia devices only.
     */
    public function experimentalWithAllGPUs(): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('experimentalWithAllGPUs');
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * EXPERIMENTAL API! Subject to change/removal at any time.
     *
     * Configures the provided list of devices to be accessible to this container.
     *
     * This currently works for Nvidia devices only.
     */
    public function experimentalWithGPU(array $devices): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('experimentalWithGPU');
        $innerQueryBuilder->setArgument('devices', $devices);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Writes the container as an OCI tarball to the destination file path on the host.
     *
     * Return true on success.
     *
     * It can also export platform variants.
     */
    public function export(
        string $path,
        ?array $platformVariants = null,
        ?ImageLayerCompression $forcedCompression = null,
        ?ImageMediaTypes $mediaTypes = null,
    ): bool
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('export');
        $leafQueryBuilder->setArgument('path', $path);
        if (null !== $platformVariants) {
        $leafQueryBuilder->setArgument('platformVariants', $platformVariants);
        }
        if (null !== $forcedCompression) {
        $leafQueryBuilder->setArgument('forcedCompression', $forcedCompression);
        }
        if (null !== $mediaTypes) {
        $leafQueryBuilder->setArgument('mediaTypes', $mediaTypes);
        }
        return (bool)$this->queryLeaf($leafQueryBuilder, 'export');
    }

    /**
     * Retrieves the list of exposed ports.
     *
     * This includes ports already exposed by the image, even if not explicitly added with dagger.
     */
    public function exposedPorts(): array
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('exposedPorts');
        return (array)$this->queryLeaf($leafQueryBuilder, 'exposedPorts');
    }

    /**
     * Retrieves a file at the given path.
     *
     * Mounts are included.
     */
    public function file(string $path): File
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('file');
        $innerQueryBuilder->setArgument('path', $path);
        return new \Dagger\File($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Initializes this container from a pulled base image.
     */
    public function from(string $address): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('from');
        $innerQueryBuilder->setArgument('address', $address);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * A unique identifier for this Container.
     */
    public function id(): ContainerId
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('id');
        return new \Dagger\ContainerId((string)$this->queryLeaf($leafQueryBuilder, 'id'));
    }

    /**
     * The unique image reference which can only be retrieved immediately after the 'Container.From' call.
     */
    public function imageRef(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('imageRef');
        return (string)$this->queryLeaf($leafQueryBuilder, 'imageRef');
    }

    /**
     * Reads the container from an OCI tarball.
     */
    public function import(FileId|File $source, ?string $tag = ''): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('import');
        $innerQueryBuilder->setArgument('source', $source);
        if (null !== $tag) {
        $innerQueryBuilder->setArgument('tag', $tag);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves the value of the specified label.
     */
    public function label(string $name): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('label');
        $leafQueryBuilder->setArgument('name', $name);
        return (string)$this->queryLeaf($leafQueryBuilder, 'label');
    }

    /**
     * Retrieves the list of labels passed to container.
     */
    public function labels(): array
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('labels');
        return (array)$this->queryLeaf($leafQueryBuilder, 'labels');
    }

    /**
     * Retrieves the list of paths where a directory is mounted.
     */
    public function mounts(): array
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('mounts');
        return (array)$this->queryLeaf($leafQueryBuilder, 'mounts');
    }

    /**
     * Creates a named sub-pipeline.
     */
    public function pipeline(string $name, ?string $description = '', ?array $labels = null): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('pipeline');
        $innerQueryBuilder->setArgument('name', $name);
        if (null !== $description) {
        $innerQueryBuilder->setArgument('description', $description);
        }
        if (null !== $labels) {
        $innerQueryBuilder->setArgument('labels', $labels);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * The platform this container executes and publishes as.
     */
    public function platform(): Platform
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('platform');
        return new \Dagger\Platform((string)$this->queryLeaf($leafQueryBuilder, 'platform'));
    }

    /**
     * Publishes this container as a new image to the specified address.
     *
     * Publish returns a fully qualified ref.
     *
     * It can also publish platform variants.
     */
    public function publish(
        string $address,
        ?array $platformVariants = null,
        ?ImageLayerCompression $forcedCompression = null,
        ?ImageMediaTypes $mediaTypes = null,
    ): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('publish');
        $leafQueryBuilder->setArgument('address', $address);
        if (null !== $platformVariants) {
        $leafQueryBuilder->setArgument('platformVariants', $platformVariants);
        }
        if (null !== $forcedCompression) {
        $leafQueryBuilder->setArgument('forcedCompression', $forcedCompression);
        }
        if (null !== $mediaTypes) {
        $leafQueryBuilder->setArgument('mediaTypes', $mediaTypes);
        }
        return (string)$this->queryLeaf($leafQueryBuilder, 'publish');
    }

    /**
     * Retrieves this container's root filesystem. Mounts are not included.
     */
    public function rootfs(): Directory
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('rootfs');
        return new \Dagger\Directory($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * The error stream of the last executed command.
     *
     * Will execute default command if none is set, or error if there's no default.
     */
    public function stderr(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('stderr');
        return (string)$this->queryLeaf($leafQueryBuilder, 'stderr');
    }

    /**
     * The output stream of the last executed command.
     *
     * Will execute default command if none is set, or error if there's no default.
     */
    public function stdout(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('stdout');
        return (string)$this->queryLeaf($leafQueryBuilder, 'stdout');
    }

    /**
     * Forces evaluation of the pipeline in the engine.
     *
     * It doesn't run the default command if no exec has been set.
     */
    public function sync(): ContainerId
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('sync');
        return new \Dagger\ContainerId((string)$this->queryLeaf($leafQueryBuilder, 'sync'));
    }

    /**
     * Return an interactive terminal for this container using its configured default terminal command if not overridden by args (or sh as a fallback default).
     */
    public function terminal(
        ?array $cmd = null,
        ?bool $experimentalPrivilegedNesting = false,
        ?bool $insecureRootCapabilities = false,
    ): Terminal
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('terminal');
        if (null !== $cmd) {
        $innerQueryBuilder->setArgument('cmd', $cmd);
        }
        if (null !== $experimentalPrivilegedNesting) {
        $innerQueryBuilder->setArgument('experimentalPrivilegedNesting', $experimentalPrivilegedNesting);
        }
        if (null !== $insecureRootCapabilities) {
        $innerQueryBuilder->setArgument('insecureRootCapabilities', $insecureRootCapabilities);
        }
        return new \Dagger\Terminal($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves the user to be set for all commands.
     */
    public function user(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('user');
        return (string)$this->queryLeaf($leafQueryBuilder, 'user');
    }

    /**
     * Configures default arguments for future commands.
     */
    public function withDefaultArgs(array $args): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withDefaultArgs');
        $innerQueryBuilder->setArgument('args', $args);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Set the default command to invoke for the container's terminal API.
     */
    public function withDefaultTerminalCmd(
        array $args,
        ?bool $experimentalPrivilegedNesting = false,
        ?bool $insecureRootCapabilities = false,
    ): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withDefaultTerminalCmd');
        $innerQueryBuilder->setArgument('args', $args);
        if (null !== $experimentalPrivilegedNesting) {
        $innerQueryBuilder->setArgument('experimentalPrivilegedNesting', $experimentalPrivilegedNesting);
        }
        if (null !== $insecureRootCapabilities) {
        $innerQueryBuilder->setArgument('insecureRootCapabilities', $insecureRootCapabilities);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container plus a directory written at the given path.
     */
    public function withDirectory(
        string $path,
        DirectoryId|Directory $directory,
        ?array $exclude = null,
        ?array $include = null,
        ?string $owner = '',
    ): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withDirectory');
        $innerQueryBuilder->setArgument('path', $path);
        $innerQueryBuilder->setArgument('directory', $directory);
        if (null !== $exclude) {
        $innerQueryBuilder->setArgument('exclude', $exclude);
        }
        if (null !== $include) {
        $innerQueryBuilder->setArgument('include', $include);
        }
        if (null !== $owner) {
        $innerQueryBuilder->setArgument('owner', $owner);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container but with a different command entrypoint.
     */
    public function withEntrypoint(array $args, ?bool $keepDefaultArgs = false): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withEntrypoint');
        $innerQueryBuilder->setArgument('args', $args);
        if (null !== $keepDefaultArgs) {
        $innerQueryBuilder->setArgument('keepDefaultArgs', $keepDefaultArgs);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container plus the given environment variable.
     */
    public function withEnvVariable(string $name, string $value, ?bool $expand = false): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withEnvVariable');
        $innerQueryBuilder->setArgument('name', $name);
        $innerQueryBuilder->setArgument('value', $value);
        if (null !== $expand) {
        $innerQueryBuilder->setArgument('expand', $expand);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container after executing the specified command inside it.
     */
    public function withExec(
        array $args,
        ?bool $skipEntrypoint = false,
        ?string $stdin = '',
        ?string $redirectStdout = '',
        ?string $redirectStderr = '',
        ?bool $experimentalPrivilegedNesting = false,
        ?bool $insecureRootCapabilities = false,
    ): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withExec');
        $innerQueryBuilder->setArgument('args', $args);
        if (null !== $skipEntrypoint) {
        $innerQueryBuilder->setArgument('skipEntrypoint', $skipEntrypoint);
        }
        if (null !== $stdin) {
        $innerQueryBuilder->setArgument('stdin', $stdin);
        }
        if (null !== $redirectStdout) {
        $innerQueryBuilder->setArgument('redirectStdout', $redirectStdout);
        }
        if (null !== $redirectStderr) {
        $innerQueryBuilder->setArgument('redirectStderr', $redirectStderr);
        }
        if (null !== $experimentalPrivilegedNesting) {
        $innerQueryBuilder->setArgument('experimentalPrivilegedNesting', $experimentalPrivilegedNesting);
        }
        if (null !== $insecureRootCapabilities) {
        $innerQueryBuilder->setArgument('insecureRootCapabilities', $insecureRootCapabilities);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Expose a network port.
     *
     * Exposed ports serve two purposes:
     *
     * - For health checks and introspection, when running services
     *
     * - For setting the EXPOSE OCI field when publishing the container
     */
    public function withExposedPort(
        int $port,
        ?NetworkProtocol $protocol = null,
        ?string $description = null,
        ?bool $experimentalSkipHealthcheck = false,
    ): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withExposedPort');
        $innerQueryBuilder->setArgument('port', $port);
        if (null !== $protocol) {
        $innerQueryBuilder->setArgument('protocol', $protocol);
        }
        if (null !== $description) {
        $innerQueryBuilder->setArgument('description', $description);
        }
        if (null !== $experimentalSkipHealthcheck) {
        $innerQueryBuilder->setArgument('experimentalSkipHealthcheck', $experimentalSkipHealthcheck);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container plus the contents of the given file copied to the given path.
     */
    public function withFile(
        string $path,
        FileId|File $source,
        ?int $permissions = null,
        ?string $owner = '',
    ): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withFile');
        $innerQueryBuilder->setArgument('path', $path);
        $innerQueryBuilder->setArgument('source', $source);
        if (null !== $permissions) {
        $innerQueryBuilder->setArgument('permissions', $permissions);
        }
        if (null !== $owner) {
        $innerQueryBuilder->setArgument('owner', $owner);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container plus the contents of the given files copied to the given path.
     */
    public function withFiles(string $path, array $sources, ?int $permissions = null, ?string $owner = ''): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withFiles');
        $innerQueryBuilder->setArgument('path', $path);
        $innerQueryBuilder->setArgument('sources', $sources);
        if (null !== $permissions) {
        $innerQueryBuilder->setArgument('permissions', $permissions);
        }
        if (null !== $owner) {
        $innerQueryBuilder->setArgument('owner', $owner);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Indicate that subsequent operations should be featured more prominently in the UI.
     */
    public function withFocus(): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withFocus');
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container plus the given label.
     */
    public function withLabel(string $name, string $value): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withLabel');
        $innerQueryBuilder->setArgument('name', $name);
        $innerQueryBuilder->setArgument('value', $value);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container plus a cache volume mounted at the given path.
     */
    public function withMountedCache(
        string $path,
        CacheVolumeId|CacheVolume $cache,
        DirectoryId|Directory|null $source = null,
        ?CacheSharingMode $sharing = null,
        ?string $owner = '',
    ): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withMountedCache');
        $innerQueryBuilder->setArgument('path', $path);
        $innerQueryBuilder->setArgument('cache', $cache);
        if (null !== $source) {
        $innerQueryBuilder->setArgument('source', $source);
        }
        if (null !== $sharing) {
        $innerQueryBuilder->setArgument('sharing', $sharing);
        }
        if (null !== $owner) {
        $innerQueryBuilder->setArgument('owner', $owner);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container plus a directory mounted at the given path.
     */
    public function withMountedDirectory(string $path, DirectoryId|Directory $source, ?string $owner = ''): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withMountedDirectory');
        $innerQueryBuilder->setArgument('path', $path);
        $innerQueryBuilder->setArgument('source', $source);
        if (null !== $owner) {
        $innerQueryBuilder->setArgument('owner', $owner);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container plus a file mounted at the given path.
     */
    public function withMountedFile(string $path, FileId|File $source, ?string $owner = ''): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withMountedFile');
        $innerQueryBuilder->setArgument('path', $path);
        $innerQueryBuilder->setArgument('source', $source);
        if (null !== $owner) {
        $innerQueryBuilder->setArgument('owner', $owner);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container plus a secret mounted into a file at the given path.
     */
    public function withMountedSecret(
        string $path,
        SecretId|Secret $source,
        ?string $owner = '',
        ?int $mode = 256,
    ): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withMountedSecret');
        $innerQueryBuilder->setArgument('path', $path);
        $innerQueryBuilder->setArgument('source', $source);
        if (null !== $owner) {
        $innerQueryBuilder->setArgument('owner', $owner);
        }
        if (null !== $mode) {
        $innerQueryBuilder->setArgument('mode', $mode);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container plus a temporary directory mounted at the given path. Any writes will be ephemeral to a single withExec call; they will not be persisted to subsequent withExecs.
     */
    public function withMountedTemp(string $path): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withMountedTemp');
        $innerQueryBuilder->setArgument('path', $path);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container plus a new file written at the given path.
     */
    public function withNewFile(
        string $path,
        ?string $contents = '',
        ?int $permissions = 420,
        ?string $owner = '',
    ): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withNewFile');
        $innerQueryBuilder->setArgument('path', $path);
        if (null !== $contents) {
        $innerQueryBuilder->setArgument('contents', $contents);
        }
        if (null !== $permissions) {
        $innerQueryBuilder->setArgument('permissions', $permissions);
        }
        if (null !== $owner) {
        $innerQueryBuilder->setArgument('owner', $owner);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container with a registry authentication for a given address.
     */
    public function withRegistryAuth(string $address, string $username, SecretId|Secret $secret): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withRegistryAuth');
        $innerQueryBuilder->setArgument('address', $address);
        $innerQueryBuilder->setArgument('username', $username);
        $innerQueryBuilder->setArgument('secret', $secret);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves the container with the given directory mounted to /.
     */
    public function withRootfs(DirectoryId|Directory $directory): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withRootfs');
        $innerQueryBuilder->setArgument('directory', $directory);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container plus an env variable containing the given secret.
     */
    public function withSecretVariable(string $name, SecretId|Secret $secret): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withSecretVariable');
        $innerQueryBuilder->setArgument('name', $name);
        $innerQueryBuilder->setArgument('secret', $secret);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Establish a runtime dependency on a service.
     *
     * The service will be started automatically when needed and detached when it is no longer needed, executing the default command if none is set.
     *
     * The service will be reachable from the container via the provided hostname alias.
     *
     * The service dependency will also convey to any files or directories produced by the container.
     */
    public function withServiceBinding(string $alias, ServiceId|Service $service): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withServiceBinding');
        $innerQueryBuilder->setArgument('alias', $alias);
        $innerQueryBuilder->setArgument('service', $service);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container plus a socket forwarded to the given Unix socket path.
     */
    public function withUnixSocket(string $path, SocketId|Socket $source, ?string $owner = ''): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withUnixSocket');
        $innerQueryBuilder->setArgument('path', $path);
        $innerQueryBuilder->setArgument('source', $source);
        if (null !== $owner) {
        $innerQueryBuilder->setArgument('owner', $owner);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container with a different command user.
     */
    public function withUser(string $name): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withUser');
        $innerQueryBuilder->setArgument('name', $name);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container with a different working directory.
     */
    public function withWorkdir(string $path): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withWorkdir');
        $innerQueryBuilder->setArgument('path', $path);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container with unset default arguments for future commands.
     */
    public function withoutDefaultArgs(): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withoutDefaultArgs');
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container with the directory at the given path removed.
     */
    public function withoutDirectory(string $path): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withoutDirectory');
        $innerQueryBuilder->setArgument('path', $path);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container with an unset command entrypoint.
     */
    public function withoutEntrypoint(?bool $keepDefaultArgs = false): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withoutEntrypoint');
        if (null !== $keepDefaultArgs) {
        $innerQueryBuilder->setArgument('keepDefaultArgs', $keepDefaultArgs);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container minus the given environment variable.
     */
    public function withoutEnvVariable(string $name): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withoutEnvVariable');
        $innerQueryBuilder->setArgument('name', $name);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Unexpose a previously exposed port.
     */
    public function withoutExposedPort(int $port, ?NetworkProtocol $protocol = null): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withoutExposedPort');
        $innerQueryBuilder->setArgument('port', $port);
        if (null !== $protocol) {
        $innerQueryBuilder->setArgument('protocol', $protocol);
        }
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container with the file at the given path removed.
     */
    public function withoutFile(string $path): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withoutFile');
        $innerQueryBuilder->setArgument('path', $path);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Indicate that subsequent operations should not be featured more prominently in the UI.
     *
     * This is the initial state of all containers.
     */
    public function withoutFocus(): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withoutFocus');
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container minus the given environment label.
     */
    public function withoutLabel(string $name): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withoutLabel');
        $innerQueryBuilder->setArgument('name', $name);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container after unmounting everything at the given path.
     */
    public function withoutMount(string $path): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withoutMount');
        $innerQueryBuilder->setArgument('path', $path);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container without the registry authentication of a given address.
     */
    public function withoutRegistryAuth(string $address): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withoutRegistryAuth');
        $innerQueryBuilder->setArgument('address', $address);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container with a previously added Unix socket removed.
     */
    public function withoutUnixSocket(string $path): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withoutUnixSocket');
        $innerQueryBuilder->setArgument('path', $path);
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container with an unset command user.
     *
     * Should default to root.
     */
    public function withoutUser(): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withoutUser');
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves this container with an unset working directory.
     *
     * Should default to "/".
     */
    public function withoutWorkdir(): Container
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withoutWorkdir');
        return new \Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Retrieves the working directory for all commands.
     */
    public function workdir(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('workdir');
        return (string)$this->queryLeaf($leafQueryBuilder, 'workdir');
    }
}
