<?php

declare(strict_types=1);

namespace Dagger\Attribute;

#[\Attribute(\Attribute::TARGET_PARAMETER | \Attribute::TARGET_PROPERTY)]
final readonly class ListOfType
{
    public function __construct(
        public ListOfType|string $type,
        public bool $nullable = false,
    ) {
    }
}
