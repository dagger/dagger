<?php

declare(strict_types=1);

namespace Dagger\tests\Unit\ValueObject;

use Dagger\Attribute;
use Dagger\Service\FindsDaggerFunctions;
use Dagger\ValueObject\DaggerFunction;
use Dagger\ValueObject\DaggerObject;
use Dagger\ValueObject\Type;
use Generator;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionClass;

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
        yield 'no methods' => (function () {
            $class = new #[Attribute\DaggerObject] class () {
            };

            return [
                new DaggerObject($class::class, []),
                new ReflectionClass($class),
            ];
        })();

        yield 'public method without DaggerFunction attribute' => (function () {
            $class = new #[Attribute\DaggerObject] class () {
                public function ignoreThis(): void
                {

                }
            };

            return [
                new DaggerObject($class::class, []),
                new ReflectionClass($class),
            ];
        })();

        yield 'private method with DaggerFunction attribute' => (function () {
            $class = new #[Attribute\DaggerObject] class () {
                #[Attribute\DaggerFunction()]
                private function ignoreThis(): void
                {

                }
            };

            return [
                new DaggerObject($class::class, []),
                new ReflectionClass($class),
            ];
        })();

        yield 'public method with DaggerFunction attribute' => (function () {
            $class = new #[Attribute\DaggerObject] class () {
                #[Attribute\DaggerFunction()]
                public function dontIgnoreThis(): void
                {

                }
            };

            return [
                new DaggerObject($class::class, [
                    new DaggerFunction(
                        'dontIgnoreThis',
                        '',
                        [],
                        new Type('void'),
                    )
                ]),
                new ReflectionClass($class),
            ];
        })();
    }
}
