<?php

/**
 * This class has been generated by dagger-php-sdk. DO NOT EDIT.
 */

declare(strict_types=1);

namespace Dagger;

/**
 * Sharing mode of the cache volume.
 */
enum CacheSharingMode: string
{
    /**
     * Shares the cache volume amongst many build pipelines,
     * but will serialize the writes
     */
    case LOCKED = 'LOCKED';

    /** Keeps a cache volume for a single build pipeline */
    case PRIVATE = 'PRIVATE';

    /** Shares the cache volume amongst many build pipelines */
    case SHARED = 'SHARED';
}
