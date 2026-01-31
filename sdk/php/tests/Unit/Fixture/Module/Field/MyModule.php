<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture\Module\Field;

use Dagger\Attribute;
use Dagger\ValueObject;

#[Attribute\Doc('Test data for implicit getters.')]
#[Attribute\DaggerObject]
class MyModule
{
    #[Attribute\DaggerFunction] public bool $boolField = true;
    #[Attribute\DaggerFunction] public float $floatField = 3.14;
    #[Attribute\DaggerFunction] public int $intField = 5;
    #[Attribute\DaggerFunction] public string $stringField = 'Hello, field!';

    public static function asValueObject(): ValueObject\DaggerObject
    {
        return new ValueObject\DaggerObject(
            self::class,
            'Test data for implicit getters.',
            [
                new ValueObject\DaggerField('boolField', '', new ValueObject\Type('bool')),
                new ValueObject\DaggerField('floatField', '', new ValueObject\Type('float')),
                new ValueObject\DaggerField('intField', '', new ValueObject\Type('int')),
                new ValueObject\DaggerField('stringField', '', new ValueObject\Type('string')),
            ],
            [],
        );
    }
}
