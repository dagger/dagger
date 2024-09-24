<?php

declare(strict_types=1);

namespace Dagger\Attribute;

#[\Attribute(\Attribute::TARGET_METHOD)]
final readonly class DaggerFunction
{
    //@TODO support renaming argument with public string $name

    /**
     * @param string|null $description deprecated, use #[Doc]
     */
    public function __construct(
        public ?string $description = null,
    ) {
    }
}
