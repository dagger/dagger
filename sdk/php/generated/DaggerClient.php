<?php

/**
 * This class has been generated by dagger-php-sdk. DO NOT EDIT.
 */

declare(strict_types=1);

namespace Dagger\Dagger;

class DaggerClient extends \Dagger\Client\AbstractDaggerClient
{
    /**
     * Constructs a cache volume for a given cache key.
     */
    public function cacheVolume(string $key): CacheVolume
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('cacheVolume');
        $innerQueryBuilder->setArgument('key', $key);
        return new \Dagger\Dagger\CacheVolume($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Checks if the current Dagger Engine is compatible with an SDK's required version.
     */
    public function checkVersionCompatibility(string $version): bool
    {
        $leafQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('checkVersionCompatibility');
        $leafQueryBuilder->setArgument('version', $version);
        return (bool)$this->queryLeaf($leafQueryBuilder, 'checkVersionCompatibility');
    }

    /**
     * Creates a scratch container or loads one by ID.
     *
     * Optional platform argument initializes new containers to execute and publish
     * as that platform. Platform defaults to that of the builder's host.
     */
    public function container(ContainerId|Container|null $id = null, ?Platform $platform = null): Container
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('container');
        if (null !== $id) {
        $innerQueryBuilder->setArgument('id', $id);
        }
        if (null !== $platform) {
        $innerQueryBuilder->setArgument('platform', $platform);
        }
        return new \Dagger\Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * The FunctionCall context that the SDK caller is currently executing in.
     * If the caller is not currently executing in a function, this will return
     * an error.
     */
    public function currentFunctionCall(): FunctionCall
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('currentFunctionCall');
        return new \Dagger\Dagger\FunctionCall($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * The module currently being served in the session, if any.
     */
    public function currentModule(): Module
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('currentModule');
        return new \Dagger\Dagger\Module($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * The default platform of the builder.
     */
    public function defaultPlatform(): Platform
    {
        $leafQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('defaultPlatform');
        return new \Dagger\Dagger\Platform((string)$this->queryLeaf($leafQueryBuilder, 'defaultPlatform'));
    }

    /**
     * Creates an empty directory or loads one by ID.
     */
    public function directory(DirectoryId|Directory|null $id = null): Directory
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('directory');
        if (null !== $id) {
        $innerQueryBuilder->setArgument('id', $id);
        }
        return new \Dagger\Dagger\Directory($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Loads a file by ID.
     */
    public function file(FileId|File $id): File
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('file');
        $innerQueryBuilder->setArgument('id', $id);
        return new \Dagger\Dagger\File($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Create a function.
     */
    public function function(string $name, TypeDefId|TypeDef $returnType): Function_
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('function');
        $innerQueryBuilder->setArgument('name', $name);
        $innerQueryBuilder->setArgument('returnType', $returnType);
        return new \Dagger\Dagger\Function_($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Create a code generation result, given a directory containing the generated
     * code.
     */
    public function generatedCode(DirectoryId|Directory $code): GeneratedCode
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('generatedCode');
        $innerQueryBuilder->setArgument('code', $code);
        return new \Dagger\Dagger\GeneratedCode($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Queries a git repository.
     */
    public function git(
        string $url,
        ?bool $keepGitDir = null,
        ?string $sshKnownHosts = null,
        SocketId|Socket|null $sshAuthSocket = null,
        ServiceId|Service|null $experimentalServiceHost = null,
    ): GitRepository
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('git');
        $innerQueryBuilder->setArgument('url', $url);
        if (null !== $keepGitDir) {
        $innerQueryBuilder->setArgument('keepGitDir', $keepGitDir);
        }
        if (null !== $sshKnownHosts) {
        $innerQueryBuilder->setArgument('sshKnownHosts', $sshKnownHosts);
        }
        if (null !== $sshAuthSocket) {
        $innerQueryBuilder->setArgument('sshAuthSocket', $sshAuthSocket);
        }
        if (null !== $experimentalServiceHost) {
        $innerQueryBuilder->setArgument('experimentalServiceHost', $experimentalServiceHost);
        }
        return new \Dagger\Dagger\GitRepository($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Queries the host environment.
     */
    public function host(): Host
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('host');
        return new \Dagger\Dagger\Host($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Returns a file containing an http remote url content.
     */
    public function http(string $url, ServiceId|Service|null $experimentalServiceHost = null): File
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('http');
        $innerQueryBuilder->setArgument('url', $url);
        if (null !== $experimentalServiceHost) {
        $innerQueryBuilder->setArgument('experimentalServiceHost', $experimentalServiceHost);
        }
        return new \Dagger\Dagger\File($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Load a CacheVolume from its ID.
     */
    public function loadCacheVolumeFromID(CacheVolumeId|CacheVolume $id): CacheVolume
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('loadCacheVolumeFromID');
        $innerQueryBuilder->setArgument('id', $id);
        return new \Dagger\Dagger\CacheVolume($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Loads a container from an ID.
     */
    public function loadContainerFromID(ContainerId|Container $id): Container
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('loadContainerFromID');
        $innerQueryBuilder->setArgument('id', $id);
        return new \Dagger\Dagger\Container($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Load a Directory from its ID.
     */
    public function loadDirectoryFromID(DirectoryId|Directory $id): Directory
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('loadDirectoryFromID');
        $innerQueryBuilder->setArgument('id', $id);
        return new \Dagger\Dagger\Directory($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Load a File from its ID.
     */
    public function loadFileFromID(FileId|File $id): File
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('loadFileFromID');
        $innerQueryBuilder->setArgument('id', $id);
        return new \Dagger\Dagger\File($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Load a function argument by ID.
     */
    public function loadFunctionArgFromID(FunctionArgId|FunctionArg $id): FunctionArg
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('loadFunctionArgFromID');
        $innerQueryBuilder->setArgument('id', $id);
        return new \Dagger\Dagger\FunctionArg($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Load a function by ID.
     */
    public function loadFunctionFromID(FunctionId|Function_ $id): Function_
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('loadFunctionFromID');
        $innerQueryBuilder->setArgument('id', $id);
        return new \Dagger\Dagger\Function_($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Load a GeneratedCode by ID.
     */
    public function loadGeneratedCodeFromID(GeneratedCodeId|GeneratedCode $id): GeneratedCode
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('loadGeneratedCodeFromID');
        $innerQueryBuilder->setArgument('id', $id);
        return new \Dagger\Dagger\GeneratedCode($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Load a git ref from its ID.
     */
    public function loadGitRefFromID(GitRefId|GitRef $id): GitRef
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('loadGitRefFromID');
        $innerQueryBuilder->setArgument('id', $id);
        return new \Dagger\Dagger\GitRef($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Load a git repository from its ID.
     */
    public function loadGitRepositoryFromID(GitRepositoryId|GitRepository $id): GitRepository
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('loadGitRepositoryFromID');
        $innerQueryBuilder->setArgument('id', $id);
        return new \Dagger\Dagger\GitRepository($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Load a module by ID.
     */
    public function loadModuleFromID(ModuleId|Module $id): Module
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('loadModuleFromID');
        $innerQueryBuilder->setArgument('id', $id);
        return new \Dagger\Dagger\Module($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Load a Secret from its ID.
     */
    public function loadSecretFromID(SecretId|Secret $id): Secret
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('loadSecretFromID');
        $innerQueryBuilder->setArgument('id', $id);
        return new \Dagger\Dagger\Secret($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Loads a service from ID.
     */
    public function loadServiceFromID(ServiceId|Service $id): Service
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('loadServiceFromID');
        $innerQueryBuilder->setArgument('id', $id);
        return new \Dagger\Dagger\Service($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Load a Socket from its ID.
     */
    public function loadSocketFromID(SocketId|Socket $id): Socket
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('loadSocketFromID');
        $innerQueryBuilder->setArgument('id', $id);
        return new \Dagger\Dagger\Socket($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Load a TypeDef by ID.
     */
    public function loadTypeDefFromID(TypeDefId|TypeDef $id): TypeDef
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('loadTypeDefFromID');
        $innerQueryBuilder->setArgument('id', $id);
        return new \Dagger\Dagger\TypeDef($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Create a new module.
     */
    public function module(): Module
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('module');
        return new \Dagger\Dagger\Module($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Load the static configuration for a module from the given source directory and optional subpath.
     */
    public function moduleConfig(DirectoryId|Directory $sourceDirectory, ?string $subpath = null): ModuleConfig
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('moduleConfig');
        $innerQueryBuilder->setArgument('sourceDirectory', $sourceDirectory);
        if (null !== $subpath) {
        $innerQueryBuilder->setArgument('subpath', $subpath);
        }
        return new \Dagger\Dagger\ModuleConfig($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Creates a named sub-pipeline.
     */
    public function pipeline(string $name, ?string $description = null, ?array $labels = null): DaggerClient
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('pipeline');
        $innerQueryBuilder->setArgument('name', $name);
        if (null !== $description) {
        $innerQueryBuilder->setArgument('description', $description);
        }
        if (null !== $labels) {
        $innerQueryBuilder->setArgument('labels', $labels);
        }
        return new \Dagger\Dagger\DaggerClient($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Loads a secret from its ID.
     */
    public function secret(SecretId|Secret $id): Secret
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('secret');
        $innerQueryBuilder->setArgument('id', $id);
        return new \Dagger\Dagger\Secret($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Sets a secret given a user defined name to its plaintext and returns the secret.
     * The plaintext value is limited to a size of 128000 bytes.
     */
    public function setSecret(string $name, string $plaintext): Secret
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('setSecret');
        $innerQueryBuilder->setArgument('name', $name);
        $innerQueryBuilder->setArgument('plaintext', $plaintext);
        return new \Dagger\Dagger\Secret($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Loads a socket by its ID.
     */
    public function socket(SocketId|Socket|null $id = null): Socket
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('socket');
        if (null !== $id) {
        $innerQueryBuilder->setArgument('id', $id);
        }
        return new \Dagger\Dagger\Socket($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Create a new TypeDef.
     */
    public function typeDef(): TypeDef
    {
        $innerQueryBuilder = new \Dagger\Client\DaggerQueryBuilder('typeDef');
        return new \Dagger\Dagger\TypeDef($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }
}
