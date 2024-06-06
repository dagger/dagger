<?php

declare(strict_types=1);

namespace Dagger\tests\Unit\ValueObject;

use Dagger\Attribute;
use Dagger\ValueObject\DaggerFunction;
use Dagger\ValueObject\DaggerArgument;
use Dagger\ValueObject\Type;
use Generator;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionMethod;

//todo finish these tests
#[CoversClass(DaggerFunction::class)]
class DaggerFunctionTest extends TestCase
{
    #[Test, DataProvider('provideReflectionMethods')]
    public function ItBuildsFromReflectionMethod(
        DaggerFunction $expected,
        ReflectionMethod $reflectionMethod,
    ): void {
        $actual = DaggerFunction::fromReflection($reflectionMethod);

        self::assertEquals($expected, $actual);
    }

    /** @return Generator<array{ 0: DaggerFunction, 1:ReflectionMethod}> */
    public static function provideReflectionMethods(): Generator
    {
        yield 'no description, no parameters' => [
            new DaggerFunction(
                'returnTrue',
                null,
                [],
                new Type('bool'),
            ),
            new ReflectionMethod(new class () {
                    #[Attribute\DaggerFunction]
                    public function returnTrue(): bool
                    {
                    }
                }, 'returnTrue'),
        ];

        yield 'description, no parameters' => [
            new DaggerFunction(
                'returnTrue',
                'read me',
                [],
                new Type('bool'),
            ),
            new ReflectionMethod(new class () {
                    #[Attribute\DaggerFunction(description: 'read me')]
                    public function returnTrue(): bool
                    {
                    }
                }, 'returnTrue'),
        ];

        yield 'no description, one parameter' => [
            new DaggerFunction(
                'echoText',
                null,
                [new DaggerArgument('text', null, new Type('string'))],
                new Type('void'),
            ),
            new ReflectionMethod(new class () {
                    #[Attribute\DaggerFunction]
                    public function echoText(string $text): void
                    {
                    }
                }, 'echoText'),
        ];
    }
}
