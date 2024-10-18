<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

/**
 * Objects that can be registered with dagger
 */
interface DaggerObject
{
    public function getName(): string;
    public function getDescription(): string;
}
