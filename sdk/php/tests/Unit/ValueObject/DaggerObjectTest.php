<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\ValueObject;

use Dagger\Tests\Unit\Fixture;
use Dagger\ValueObject\DaggerObject;
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

    /** @return \Generator<array{ 0: DaggerObject, 1:ReflectionClass}> */
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
