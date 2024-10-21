<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture\Enum;

use Dagger\Attribute;
use Dagger\ValueObject;

#[Attribute\DaggerObject]
#[Attribute\Doc('A string-backed enum created for testing purposes')]
enum StringBackedDummy: string
{
    #[Attribute\Doc('This case value is "hello, "')]
    case Hello = 'hello, ';

    #[Attribute\Doc('This case value is "world!"')]
    case World = 'world!';

    public static function getValueObjectEquivalent(): ValueObject\DaggerEnum
    {
        return new ValueObject\DaggerEnum(
            self::class,
            'A string-backed enum created for testing purposes',
            [
                'Hello' => 'This case value is "hello, "',
                'World' => 'This case value is "world!"',
            ]
        );
    }
}
