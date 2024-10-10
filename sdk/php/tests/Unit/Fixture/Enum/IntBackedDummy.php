<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture\Enum;

use Dagger\Attribute;
use Dagger\ValueObject;

#[Attribute\DaggerObject]
#[Attribute\Doc('An int-backed enum created for testing purposes')]
enum IntBackedDummy: int
{
    #[Attribute\Doc('This case value is 0')]
    case Zero = 0;

    #[Attribute\Doc('This case value is 1')]
    case One = 1;

    public static function getValueObjectEquivalent(): ValueObject\DaggerEnum
    {
        return new ValueObject\DaggerEnum(
            self::class,
            'An int-backed enum created for testing purposes',
            [
                'Zero' => 'This case value is 0',
                'One' => 'This case value is 1',
            ]
        );
    }
}
