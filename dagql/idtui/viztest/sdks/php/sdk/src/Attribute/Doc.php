<?php

declare(strict_types=1);

namespace Dagger\Attribute;

use Attribute;

#[Attribute(
    Attribute::TARGET_CLASS |
    Attribute::TARGET_CLASS_CONSTANT |
    Attribute::TARGET_METHOD |
    Attribute::TARGET_PARAMETER |
    Attribute::TARGET_PROPERTY
)]
final readonly class Doc
{
    public function __construct(
        public string $description,
    ) {
    }
}
