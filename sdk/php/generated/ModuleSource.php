<?php

/**
 * This class has been generated by dagger-php-sdk. DO NOT EDIT.
 */

declare(strict_types=1);

namespace Dagger;

/**
 * The source needed to load and run a module, along with any metadata about the source such as versions/urls/etc.
 */
class ModuleSource extends Client\AbstractObject implements Client\IdAble
{
    /**
     * If the source is a of kind git, the git source representation of it.
     */
    public function asGitSource(): GitModuleSource
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('asGitSource');
        return new \Dagger\GitModuleSource($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * If the source is of kind local, the local source representation of it.
     */
    public function asLocalSource(): LocalModuleSource
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('asLocalSource');
        return new \Dagger\LocalModuleSource($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Load the source as a module. If this is a local source, the parent directory must have been provided during module source creation
     */
    public function asModule(?string $engineVersion = null): Module
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('asModule');
        if (null !== $engineVersion) {
        $innerQueryBuilder->setArgument('engineVersion', $engineVersion);
        }
        return new \Dagger\Module($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * A human readable ref string representation of this module source.
     */
    public function asString(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('asString');
        return (string)$this->queryLeaf($leafQueryBuilder, 'asString');
    }

    /**
     * Returns whether the module source has a configuration file.
     */
    public function configExists(): bool
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('configExists');
        return (bool)$this->queryLeaf($leafQueryBuilder, 'configExists');
    }

    /**
     * The directory containing everything needed to load and use the module.
     */
    public function contextDirectory(): Directory
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('contextDirectory');
        return new \Dagger\Directory($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * The effective module source dependencies from the configuration, and calls to withDependencies and withoutDependencies.
     */
    public function dependencies(): array
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('dependencies');
        return (array)$this->queryLeaf($leafQueryBuilder, 'dependencies');
    }

    /**
     * Return the module source's content digest. The format of the digest is not guaranteed to be stable between releases of Dagger. It is guaranteed to be stable between invocations of the same Dagger engine.
     */
    public function digest(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('digest');
        return (string)$this->queryLeaf($leafQueryBuilder, 'digest');
    }

    /**
     * The directory containing the module configuration and source code (source code may be in a subdir).
     */
    public function directory(string $path): Directory
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('directory');
        $innerQueryBuilder->setArgument('path', $path);
        return new \Dagger\Directory($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * A unique identifier for this ModuleSource.
     */
    public function id(): ModuleSourceId
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('id');
        return new \Dagger\ModuleSourceId((string)$this->queryLeaf($leafQueryBuilder, 'id'));
    }

    /**
     * The kind of source (e.g. local, git, etc.)
     */
    public function kind(): ModuleSourceKind
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('kind');
        return \Dagger\ModuleSourceKind::from((string)$this->queryLeaf($leafQueryBuilder, 'kind'));
    }

    /**
     * If set, the name of the module this source references, including any overrides at runtime by callers.
     */
    public function moduleName(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('moduleName');
        return (string)$this->queryLeaf($leafQueryBuilder, 'moduleName');
    }

    /**
     * The original name of the module this source references, as defined in the module configuration.
     */
    public function moduleOriginalName(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('moduleOriginalName');
        return (string)$this->queryLeaf($leafQueryBuilder, 'moduleOriginalName');
    }

    /**
     * The path to the module source's context directory on the caller's filesystem. Only valid for local sources.
     */
    public function resolveContextPathFromCaller(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('resolveContextPathFromCaller');
        return (string)$this->queryLeaf($leafQueryBuilder, 'resolveContextPathFromCaller');
    }

    /**
     * Resolve the provided module source arg as a dependency relative to this module source.
     */
    public function resolveDependency(ModuleSourceId|ModuleSource $dep): ModuleSource
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('resolveDependency');
        $innerQueryBuilder->setArgument('dep', $dep);
        return new \Dagger\ModuleSource($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Load a directory from the caller optionally with a given view applied.
     */
    public function resolveDirectoryFromCaller(
        string $path,
        ?string $viewName = null,
        ?array $ignore = null,
    ): Directory {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('resolveDirectoryFromCaller');
        $innerQueryBuilder->setArgument('path', $path);
        if (null !== $viewName) {
        $innerQueryBuilder->setArgument('viewName', $viewName);
        }
        if (null !== $ignore) {
        $innerQueryBuilder->setArgument('ignore', $ignore);
        }
        return new \Dagger\Directory($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Load the source from its path on the caller's filesystem, including only needed+configured files and directories. Only valid for local sources.
     */
    public function resolveFromCaller(): ModuleSource
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('resolveFromCaller');
        return new \Dagger\ModuleSource($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * The path relative to context of the root of the module source, which contains dagger.json. It also contains the module implementation source code, but that may or may not being a subdir of this root.
     */
    public function sourceRootSubpath(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('sourceRootSubpath');
        return (string)$this->queryLeaf($leafQueryBuilder, 'sourceRootSubpath');
    }

    /**
     * The path relative to context of the module implementation source code.
     */
    public function sourceSubpath(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('sourceSubpath');
        return (string)$this->queryLeaf($leafQueryBuilder, 'sourceSubpath');
    }

    /**
     * Retrieve a named view defined for this module source.
     */
    public function view(string $name): ModuleSourceView
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('view');
        $innerQueryBuilder->setArgument('name', $name);
        return new \Dagger\ModuleSourceView($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * The named views defined for this module source, which are sets of directory filters that can be applied to directory arguments provided to functions.
     */
    public function views(): array
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('views');
        return (array)$this->queryLeaf($leafQueryBuilder, 'views');
    }

    /**
     * Update the module source with a new context directory. Only valid for local sources.
     */
    public function withContextDirectory(DirectoryId|Directory $dir): ModuleSource
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withContextDirectory');
        $innerQueryBuilder->setArgument('dir', $dir);
        return new \Dagger\ModuleSource($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Append the provided dependencies to the module source's dependency list.
     */
    public function withDependencies(array $dependencies): ModuleSource
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withDependencies');
        $innerQueryBuilder->setArgument('dependencies', $dependencies);
        return new \Dagger\ModuleSource($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Sets module init arguments
     */
    public function withInit(?bool $merge = false): ModuleSource
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withInit');
        if (null !== $merge) {
        $innerQueryBuilder->setArgument('merge', $merge);
        }
        return new \Dagger\ModuleSource($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Update the module source with a new name.
     */
    public function withName(string $name): ModuleSource
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withName');
        $innerQueryBuilder->setArgument('name', $name);
        return new \Dagger\ModuleSource($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Update the module source with a new SDK.
     */
    public function withSDK(string $sdk): ModuleSource
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withSDK');
        $innerQueryBuilder->setArgument('sdk', $sdk);
        return new \Dagger\ModuleSource($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Update the module source with a new source subpath.
     */
    public function withSourceSubpath(string $path): ModuleSource
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withSourceSubpath');
        $innerQueryBuilder->setArgument('path', $path);
        return new \Dagger\ModuleSource($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Update the module source with a new named view.
     */
    public function withView(string $name, array $patterns): ModuleSource
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withView');
        $innerQueryBuilder->setArgument('name', $name);
        $innerQueryBuilder->setArgument('patterns', $patterns);
        return new \Dagger\ModuleSource($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Remove the provided dependencies from the module source's dependency list.
     */
    public function withoutDependencies(array $dependencies): ModuleSource
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withoutDependencies');
        $innerQueryBuilder->setArgument('dependencies', $dependencies);
        return new \Dagger\ModuleSource($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }
}
