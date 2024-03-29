<?php

/**
 * This class has been generated by dagger-php-sdk. DO NOT EDIT.
 */

declare(strict_types=1);

namespace Dagger;

/**
 * A directory whose contents persist across runs.
 */
class CacheVolume extends Client\AbstractObject implements Client\IdAble
{
    /**
     * A unique identifier for this CacheVolume.
     */
    public function id(): CacheVolumeId
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('id');
        return new \Dagger\CacheVolumeId((string)$this->queryLeaf($leafQueryBuilder, 'id'));
    }
}
