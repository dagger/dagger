<?php

declare(strict_types=1);

namespace Dagger\Attribute;

#[\Attribute(\Attribute::TARGET_METHOD)]
final readonly class ReturnsListOfType
{
    public function __construct(
        public ListOfType|string $type,
        public bool $nullable = false,
    ) {
    }
}
