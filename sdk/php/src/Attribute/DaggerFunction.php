<?php

declare(strict_types=1);

namespace Dagger\Attribute;

#[\Attribute(\Attribute::TARGET_METHOD)]
final class DaggerFunction
{
    //@TODO support renaming argument with public string $name
    public function __construct(
        public ?string $description = null,
    ) {
    }
}
