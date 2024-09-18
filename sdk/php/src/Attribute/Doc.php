<?php

declare(strict_types=1);

namespace Dagger\Attribute;

use Attribute;

#[Attribute(
    Attribute::TARGET_CLASS |
    Attribute::TARGET_METHOD |
    Attribute::TARGET_PARAMETER
)]
final readonly class Doc
{
    public function __construct(
        public string $description,
    ) {
    }
}
