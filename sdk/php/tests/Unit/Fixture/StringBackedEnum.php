<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture;

use Dagger\Attribute;
use Dagger\ValueObject;

#[Attribute\DaggerObject]
#[Attribute\Doc('An enumeration of nonsense words.')]
enum StringBackedEnum: string
{
    #[Attribute\Doc('The first nonsense word.')]
    case Foo = 'first nonsense word';

    // The (undocumented) second nonsense word.
    case Bar = 'second nonsense word';

    #[Attribute\Doc('The third nonsense word.')]
    case Baz = 'third nonsense word';

    public static function asValueObject(): ValueObject\DaggerEnum
    {
        return new ValueObject\DaggerEnum(
            self::class,
            'An enumeration of nonsense words.',
            [
                new ValueObject\DaggerEnumCase(
                    name: 'Foo',
                    value: 'first nonsense word',
                    description: 'The first nonsense word.',
                ),
                new ValueObject\DaggerEnumCase(
                    name: 'Bar',
                    value: 'second nonsense word',
                    description: '',
                ),
                new ValueObject\DaggerEnumCase(
                    name: 'Baz',
                    value: 'third nonsense word',
                    description: 'The third nonsense word.',
                ),
            ],
        );
    }
}
