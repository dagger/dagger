<?php

declare(strict_types=1);

namespace Dagger\Attribute;

#[\Attribute(\Attribute::TARGET_PARAMETER)]
final class DaggerArgument
{
    //@TODO support renaming argumet with public string $name
    public function __construct(
        public ?string $description = null,
    ) {
    }
}
