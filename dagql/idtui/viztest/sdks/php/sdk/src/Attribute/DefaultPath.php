<?php

declare(strict_types=1);

namespace Dagger\Attribute;

#[\Attribute(\Attribute::TARGET_PARAMETER)]
final readonly class DefaultPath
{
    public function __construct(
        public string $path,
    ) {
    }
}
