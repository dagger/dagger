<?php

declare(strict_types=1);

namespace Dagger\Attribute;

/** @deprecated use #[Doc]*/
#[\Attribute(\Attribute::TARGET_PARAMETER)]
final readonly class Argument
{
    //@TODO support renaming argument with public string $name
    public function __construct(
        public ?string $description = null,
    ) {
    }
}
