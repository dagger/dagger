<?php

declare(strict_types=1);

namespace Dagger\Attribute;

#[\Attribute(\Attribute::TARGET_PARAMETER)]
final readonly class Ignore
{
    /** @var string[] */
    public array $ignore;

    public function __construct(
        string ...$ignore,
    ) {
        $this->ignore = $ignore;
    }
}
