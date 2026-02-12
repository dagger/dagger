<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\ValueObject;

use Dagger\Tests\Unit\Fixture;
use Dagger\ValueObject\DaggerField;
use Dagger\ValueObject\DaggerFunction;
use Dagger\ValueObject\DaggerObject;
use Dagger\ValueObject\Type;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\Attributes\TestDox;
use PHPUnit\Framework\TestCase;
use ReflectionClass;

#[Group('unit')]
#[CoversClass(DaggerObject::class)]
class DaggerObjectTest extends TestCase
{
    /**
     * @param DaggerField[] $fields
     */
    #[Test]
    #[TestDox('Objects with fields must be initialized i.e. constructed')]
    #[DataProvider('provideFieldsThatRequireConstruction')]
    public function ItMayRequireConstruction(
        array $fields,
        bool $expected,
    ): void {
        $sut = new DaggerObject('', '', $fields, []);

        self::assertEquals($expected, $sut->requiresConstruction());
    }

    /**
     * @param DaggerFunction[] $functions
     */
    #[Test]
    #[DataProvider('provideFunctionsThatMayContainConstructor')]
    public function ItMayHaveConstructor(
        array $functions,
        bool $expected,
    ): void {
        $sut = new DaggerObject('', '', [], $functions);

        self::assertEquals($expected, $sut->hasConstructor());
    }


    #[Test, DataProvider('provideReflectionClasses')]
    public function ItBuildsFromReflectionClass(
        DaggerObject $expected,
        ReflectionClass $reflectionClass,
    ): void {
        $actual = DaggerObject::fromReflection($reflectionClass);

        self::assertEquals($expected, $actual);
    }

    /** @return \Generator<array{ DaggerObject, ReflectionClass}> */
    public static function provideFieldsThatRequireConstruction(): \Generator
    {
        yield 'no fields' => [[], false];
        yield '1 field' => [
            [new DaggerField('foo', '', new Type('string'))],
            true,
        ];

        yield '3 fields' => [
            [
                new DaggerField('foo', '', new Type('string')),
                new DaggerField('bar', '', new Type('int')),
                new DaggerField('baz', '', new Type('float')),
            ],
            true,
        ];
    }

    /** @return \Generator<array{ DaggerObject, ReflectionClass}> */
    public static function provideFunctionsThatMayContainConstructor(): \Generator
    {
        yield 'no functions' => [[], false];
        yield 'has constructor only' => [
            [new DaggerFunction('', '', [], new Type(DaggerObject::class))],
            true,
        ];

        yield 'has normal functions only' => [
            [
                new DaggerFunction('foo', '', [], new Type('string')),
            ],
            false,
        ];

        yield 'has normal functions and constructor' => [
            [
                new DaggerFunction('foo', '', [], new Type('string')),
                new DaggerFunction('bar', '', [], new Type('int')),
                new DaggerFunction('baz', '', [], new Type('float')),
                new DaggerFunction('', '', [], new Type(DaggerObject::class)),
            ],
            true,
        ];
    }

    /** @return \Generator<array{ DaggerObject, ReflectionClass}> */
    public static function provideReflectionClasses(): \Generator
    {
        yield 'DaggerObject without DaggerFunctions' => [
            Fixture\NoDaggerFunctions::getValueObjectEquivalent(),
            new ReflectionClass(Fixture\NoDaggerFunctions::class),
        ];

        yield 'DaggerObject with DaggerFunctions' => [
            Fixture\DaggerObjectWithDaggerFunctions::getValueObjectEquivalent(),
            new ReflectionClass(Fixture\DaggerObjectWithDaggerFunctions::class),
        ];

        yield 'Dagger Fields' => [
            Fixture\Module\Field\MyModule::asValueObject(),
            new ReflectionClass(Fixture\Module\Field\MyModule::class),
        ];
    }
}
