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
    public function asGitSource(): GitModuleSource
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('asGitSource');
        return new \Dagger\GitModuleSource($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    public function asLocalSource(): LocalModuleSource
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('asLocalSource');
        return new \Dagger\LocalModuleSource($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Load the source as a module. If this is a local source, the parent directory must have been provided during module source creation
     */
    public function asModule(): Module
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('asModule');
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
     * TODO
     */
    public function directory(?string $path = '/'): Directory
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('directory');
        if (null !== $path) {
        $innerQueryBuilder->setArgument('path', $path);
        }
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

    public function kind(): ModuleSourceKind
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('kind');
        return \Dagger\ModuleSourceKind::from((string)$this->queryLeaf($leafQueryBuilder, 'kind'));
    }

    /**
     * If set, the name of the module this source references
     */
    public function moduleName(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('moduleName');
        return (string)$this->queryLeaf($leafQueryBuilder, 'moduleName');
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

    public function rootDirectory(): Directory
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('rootDirectory');
        return new \Dagger\Directory($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * The path to the module subdirectory containing the actual module's source code.
     */
    public function subpath(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('subpath');
        return (string)$this->queryLeaf($leafQueryBuilder, 'subpath');
    }
}
