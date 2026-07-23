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
    case Foo = 'FOO';

    // The (undocumented) second nonsense word.
    case Bar = 'BAR';

    #[Attribute\Doc('The third nonsense word.')]
    case Baz = 'BAZ';

    public static function asValueObject(): ValueObject\DaggerEnum
    {
        return new ValueObject\DaggerEnum(
            self::class,
            'An enumeration of nonsense words.',
            [
                new ValueObject\DaggerEnumCase(
                    name: 'Foo',
                    value: 'FOO',
                    description: 'The first nonsense word.',
                ),
                new ValueObject\DaggerEnumCase(
                    name: 'Bar',
                    value: 'BAR',
                    description: '',
                ),
                new ValueObject\DaggerEnumCase(
                    name: 'Baz',
                    value: 'BAZ',
                    description: 'The third nonsense word.',
                ),
            ],
        );
    }
}
