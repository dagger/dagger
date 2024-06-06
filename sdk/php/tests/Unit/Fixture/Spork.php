<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture;

use Dagger\Attribute\DaggerFunction;

final class Spork
{
    #[DaggerFunction]
    public function poke(string $thingPoked): string
    {
        return sprintf('You poked %s!', $thingPoked);
    }
}
