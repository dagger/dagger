<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\ValueObject;

#[DaggerObject]
class DaggerObjectUsingEnums
{
    #[DaggerFunction]
    #[Doc('Cycles through the sequence; FOO, BAR, BAZ.')]
    public function cycleNonsenseWords(
        StringBackedEnum $nonsenseWord,
    ): StringBackedEnum {
        return match($nonsenseWord) {
            StringBackedEnum::Foo => StringBackedEnum::Bar,
            StringBackedEnum::Bar => StringBackedEnum::Baz,
            StringBackedEnum::Baz => StringBackedEnum::Foo,
        };
    }

    public static function getValueObjectEquivalent(): ValueObject\DaggerObject
    {
        $stringBackedEnum = new ValueObject\Type(StringBackedEnum::class);

        return new ValueObject\DaggerObject(
            name: self::class,
            description: '',
            fields: [],
            functions: [
                new ValueObject\DaggerFunction(
                    name: 'cycleNonsenseWords',
                    description: 'Cycles through the sequence; FOO, BAR, BAZ.',
                    arguments: [new ValueObject\Argument(
                        name: 'nonsenseWord',
                        description: '',
                        type: $stringBackedEnum,
                    )],
                    returnType: $stringBackedEnum,
                ),
            ],
        );
    }
}
