<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\ValueObject;

use Dagger\Tests\Unit\Fixture\DaggerObjectWithDaggerFunctions;
use Dagger\Tests\Unit\Fixture\DaggerObject\HandlingEnums;
use Dagger\ValueObject\DaggerClass;
use Dagger\Tests\Unit\Fixture\NoDaggerFunctions;
use Generator;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionClass;
use RuntimeException;

#[Group('unit')]
#[CoversClass(DaggerClass::class)]
class DaggerClassTest extends TestCase
{
    #[Test]
    public function itOnlyReflectsDaggerObjects(): void
    {
        $reflection = new ReflectionClass(self::class);

        self::expectException(RuntimeException::class);

        DaggerClass::fromReflection($reflection);
    }

    #[Test]
    #[DataProvider('provideReflectionClasses')]
    public function itBuildsFromReflectionClass(
        DaggerClass $expected,
        ReflectionClass $reflectionClass,
    ): void {
        $actual = DaggerClass::fromReflection($reflectionClass);

        self::assertEquals($expected, $actual);
    }

    /** @return Generator<array{ 0: DaggerClass, 1:ReflectionClass}> */
    public static function provideReflectionClasses(): Generator
    {
        yield 'DaggerClass without DaggerFunctions' => [
            NoDaggerFunctions::getValueObjectEquivalent(),
            new ReflectionClass(NoDaggerFunctions::class),
        ];

        yield 'DaggerClass with DaggerFunctions' => [
            DaggerObjectWithDaggerFunctions::getValueObjectEquivalent(),
            new ReflectionClass(DaggerObjectWithDaggerFunctions::class),
        ];

        yield 'HandlingEnums' => [
            HandlingEnums::getValueObjectEquivalent(),
            new ReflectionClass(HandlingEnums::class),
        ];
    }
}
