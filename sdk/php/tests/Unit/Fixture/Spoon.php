<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture;

use Dagger\Attribute\DaggerFunction;

final class Spoon
{
    public function bend(bool $withMind): bool
    {
        return !$withMind;
    }
}
