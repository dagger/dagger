<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture;

use Dagger\Attribute;
use Dagger\ValueObject;

#[Attribute\DaggerObject]
#[Attribute\Doc('An enumeration backed by integers.')]
enum IntBackedEnum: int
{
    #[Attribute\Doc('The lesser of two evils.')]
    case Low = 1;

    case High = 10;

    public static function asValueObject(): ValueObject\DaggerEnum
    {
        return new ValueObject\DaggerEnum(
            self::class,
            'An enumeration backed by integers.',
            [
                new ValueObject\DaggerEnumCase(
                    name: 'Low',
                    value: '1',
                    description: 'The lesser of two evils.',
                ),
                new ValueObject\DaggerEnumCase(
                    name: 'High',
                    value: '10',
                    description: '',
                ),
            ],
        );
    }
}
