<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\ValueObject;

use Dagger\Tests\Unit\Fixture\DaggerObjectWithDaggerFunctions;
use Dagger\Tests\Unit\Fixture\NoDaggerFunctions;
use Dagger\ValueObject\DaggerObject;
use Generator;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionClass;

#[Group('unit')]
#[CoversClass(DaggerObject::class)]
class DaggerObjectTest extends TestCase
{
    #[Test, DataProvider('provideReflectionClasses')]
    public function ItBuildsFromReflectionClass(
        DaggerObject $expected,
        ReflectionClass $reflectionClass,
    ): void {
        $actual = DaggerObject::fromReflection($reflectionClass);

        self::assertEquals($expected, $actual);
    }

    /** @return Generator<array{ 0: DaggerObject, 1:ReflectionClass}> */
    public static function provideReflectionClasses(): Generator
    {
        yield 'DaggerObject without DaggerFunctions' => [
            NoDaggerFunctions::getValueObjectEquivalent(),
            new ReflectionClass(NoDaggerFunctions::class),
        ];

        yield 'DaggerObject with DaggerFunctions' => [
            DaggerObjectWithDaggerFunctions::getValueObjectEquivalent(),
            new ReflectionClass(DaggerObjectWithDaggerFunctions::class),
        ];
    }
}
