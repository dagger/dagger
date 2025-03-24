<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerFunction, DaggerObject};

#[DaggerObject] class ScalarKind
{
    #[DaggerFunction] public function oppositeBool(bool $arg): bool
    {
        return !$arg;
    }

    #[DaggerFunction] public function halfFloat(float $arg): float
    {
        return $arg / 2;
    }

    #[DaggerFunction] public function doubleInt(int $arg): int
    {
        return $arg * 2;
    }

    #[DaggerFunction] public function capitalizeString(string $arg): string
    {
        return ucwords($arg);
    }
}
