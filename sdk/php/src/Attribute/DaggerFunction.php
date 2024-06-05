<?php

declare(strict_types=1);

namespace Dagger\Attribute;

#[\Attribute(\Attribute::TARGET_METHOD)]
final class DaggerFunction
{
    public function __construct(
        public ?string $name = null,
        public ?string $documentation = null,
    ) {
    }
}