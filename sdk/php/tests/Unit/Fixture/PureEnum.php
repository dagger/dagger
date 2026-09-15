<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture;

use Dagger\Attribute;
use Dagger\ValueObject;

#[Attribute\DaggerObject]
#[Attribute\Doc('An enumeration with no backing type.')]
enum PureEnum
{
    #[Attribute\Doc('The lesser of two evils.')]
    case Low;

    case High;

    public static function asValueObject(): ValueObject\DaggerEnum
    {
        return new ValueObject\DaggerEnum(
            self::class,
            'An enumeration with no backing type.',
            [
                new ValueObject\DaggerEnumCase(
                    name: 'Low',
                    value: '',
                    description: 'The lesser of two evils.',
                ),
                new ValueObject\DaggerEnumCase(
                    name: 'High',
                    value: '',
                    description: '',
                ),
            ],
        );
    }
}
