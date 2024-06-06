<?php

declare(strict_types=1);

namespace Dagger\tests\Unit\ValueObject;

use Dagger\Container;
use Dagger\Directory;
use Dagger\File;
use Dagger\ValueObject\DaggerArgument;
use Dagger\ValueObject\Type;
use Generator;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionMethod;
use ReflectionNamedType;
use ReflectionParameter;
use ReflectionType;

#[CoversClass(DaggerArgument::class)]
class DaggerArgumentTest extends TestCase
{
    #[Test]
    #[DataProvider('provideReflectionParameters')]
    public function ItBuildsFromReflectionParameter(
        DaggerArgument $expected,
        ReflectionParameter $reflectionParameter,
    ): void {
        $actual = DaggerArgument::fromReflection($reflectionParameter);

        self::assertEquals($expected, $actual);
    }

    /** @return Generator<array{ 0: Type, 1:ReflectionNamedType}> */
    public static function provideReflectionParameters(): Generator
    {
        yield 'array parameter without description' =>  [
            new DaggerArgument('param', null, new Type('array')),
            self::getReflectionParameter(new class() {
                public function method(array $param): void
                {
                }
            }, 'method', 'param'),
        ];

        yield 'bool parameter with description' =>  [
            new DaggerArgument('param', 'true or false', new Type('bool')),
            self::getReflectionParameter(new class() {
                public function method(
                    #[\Dagger\Attribute\DaggerArgument('true or false')]
                    bool $param
                ): void {
                }
            }, 'method', 'param'),
        ];

        yield 'float parameter without description' =>  [
            new DaggerArgument('param', null, new Type('float')),
            self::getReflectionParameter(new class() {
                public function method(float $param): void
                {
                }
            }, 'method', 'param'),
        ];

        yield 'int parameter, with description' =>  [
            new DaggerArgument('param', 'A whole number', new Type('int')),
            self::getReflectionParameter(new class() {
                public function method(
                    #[\Dagger\Attribute\DaggerArgument('A whole number')]
                    int $param
                ): void {
                }
            }, 'method', 'param'),
        ];

        yield 'string parameter without description' =>  [
            new DaggerArgument('param', null, new Type('string')),
            self::getReflectionParameter(new class() {
                public function method(string $param): void
                {
                }
            }, 'method', 'param'),
        ];

        yield 'Container parameter' =>  [
            new DaggerArgument(
                'param',
                'Container to run',
                new Type(Container::class)
            ),
            self::getReflectionParameter(new class() {
                public function method(
                    #[\Dagger\Attribute\DaggerArgument('Container to run')]
                    Container $param
                ): void
                {
                }
            }, 'method', 'param'),
        ];

        yield 'Directory parameter without description' =>  [
            new DaggerArgument('param', null, new Type(Directory::class)),
            self::getReflectionParameter(new class() {
                public function method(Directory $param): void
                {
                }
            }, 'method', 'param'),
        ];

        yield 'File parameter without description' =>  [
            new DaggerArgument('param', null, new Type(File::class)),
            self::getReflectionParameter(new class() {
                public function method(File $param): void
                {
                }
            }, 'method', 'param'),
        ];
    }

    private static function getReflectionParameter(
        object $class,
        string $method,
        string $parameter,
    ): ReflectionParameter {
        $parameters = (new ReflectionMethod($class, $method))->getParameters();

        return array_filter($parameters, fn($p) => $p->name === $parameter)[0];
    }
}
