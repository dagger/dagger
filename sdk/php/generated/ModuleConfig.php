<?php

/**
 * This class has been generated by dagger-php-sdk. DO NOT EDIT.
 */

declare(strict_types=1);

namespace DaggerIo\Gen;

/**
 * Static configuration for a module (e.g. parsed contents of dagger.json)
 */
class ModuleConfig extends \DaggerIo\Client\AbstractDaggerObject
{
    /**
     * Modules that this module depends on.
     */
    public function dependencies(): array
    {
        $leafQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('dependencies');
        return $this->queryLeaf($leafQueryBuilder, 'dependencies');
    }

    /**
     * Exclude these file globs when loading the module root.
     */
    public function exclude(): array
    {
        $leafQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('exclude');
        return $this->queryLeaf($leafQueryBuilder, 'exclude');
    }

    /**
     * Include only these file globs when loading the module root.
     */
    public function include(): array
    {
        $leafQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('include');
        return $this->queryLeaf($leafQueryBuilder, 'include');
    }

    /**
     * The name of the module.
     */
    public function name(): string
    {
        $leafQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('name');
        return $this->queryLeaf($leafQueryBuilder, 'name');
    }

    /**
     * The root directory of the module's project, which may be above the module source code.
     */
    public function root(): string
    {
        $leafQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('root');
        return $this->queryLeaf($leafQueryBuilder, 'root');
    }

    /**
     * Either the name of a built-in SDK ('go', 'python', etc.) OR a module reference pointing to the SDK's module implementation.
     */
    public function sdk(): string
    {
        $leafQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('sdk');
        return $this->queryLeaf($leafQueryBuilder, 'sdk');
    }
}
