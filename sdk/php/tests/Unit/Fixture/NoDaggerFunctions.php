<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture;

use Dagger\Attribute\DaggerObject;
use Dagger\ValueObject;

#[DaggerObject]
final class NoDaggerFunctions
{
    public function notDaggerFunction(): bool
    {
        return true;
    }

    public static function getValueObjectEquivalent(): ValueObject\DaggerObject
    {
        return new ValueObject\DaggerObject(self::class, []);
    }
}
