<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\ValueObject;

use Dagger\Container;
use Dagger\File;
use Dagger\Json;
use Dagger\Tests\Unit\Fixture\DaggerObjectWithDaggerFunctions;
use Dagger\ValueObject\Argument;
use Dagger\ValueObject\DaggerFunction;
use Dagger\ValueObject\DaggerObject;
use Dagger\ValueObject\Type;
use Dagger\Tests\Unit\Fixture\NoDaggerFunctions;
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
