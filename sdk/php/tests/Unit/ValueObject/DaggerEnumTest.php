<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\ValueObject;

use Dagger\Tests\Unit\Fixture\DaggerObject\HandlingEnums;
use Dagger\Tests\Unit\Fixture\DaggerObjectWithDaggerFunctions;
use Dagger\Tests\Unit\Fixture\Enum\IntBackedDummy;
use Dagger\Tests\Unit\Fixture\Enum\NotDaggerObject;
use Dagger\Tests\Unit\Fixture\Enum\StringBackedDummy;
use Dagger\Tests\Unit\Fixture\Enum\UnitDummy;
use Dagger\Tests\Unit\Fixture\NoDaggerFunctions;
use Dagger\ValueObject\DaggerClass;
use Dagger\ValueObject\DaggerEnum;
use Generator;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionClass;
use ReflectionEnum;
use RuntimeException;

#[Group('unit')]
#[CoversClass(DaggerEnum::class)]
class DaggerEnumTest extends TestCase
{
    #[Test]
    public function itOnlyReflectsDaggerObjects(): void
    {
        $reflection = new ReflectionEnum(NotDaggerObject::class);

        self::expectException(RuntimeException::class);

        DaggerEnum::fromReflection($reflection);
    }

    #[Test]
    #[DataProvider('provideReflectionEnums')]
    public function itBuildsFromReflectionEnum(
        DaggerEnum $expected,
        ReflectionEnum $reflectionEnum,
    ): void {
        $actual = DaggerEnum::fromReflection($reflectionEnum);

        self::assertEquals($expected, $actual);
    }

    /** @return Generator<array{ 0: DaggerEnum, 1:ReflectionEnum}> */
    public static function provideReflectionEnums(): Generator
    {
        yield 'string backed enum' => [
            StringBackedDummy::getValueObjectEquivalent(),
            new ReflectionEnum(StringBackedDummy::class),
        ];

        yield 'int backed enum' => [
            IntBackedDummy::getValueObjectEquivalent(),
            new ReflectionEnum(IntBackedDummy::class),
        ];

        yield 'unit enum' => [
            UnitDummy::getValueObjectEquivalent(),
            new ReflectionEnum(UnitDummy::class),
        ];
    }
}
