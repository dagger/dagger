<?php

declare(strict_types=1);

namespace Dagger\tests\Unit\ValueObject;

use Countable;
use Dagger\Container;
use Dagger\Directory;
use Dagger\File;
use Dagger\ValueObject\Type;
use Generator;
use Iterator;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionMethod;
use ReflectionNamedType;
use ReflectionType;
use RuntimeException;

#[CoversClass(Type::class)]
class TypeTest extends TestCase
{
    #[Test]
    public function itCannotBuildFromReflectionUnionTypes(): void
    {
        $reflectionType = self::getReflectionOfReturnType(new class () {
            public function method(): bool|string
            {
                return true;
            }
        }, 'method');

        self::expectException(RuntimeException::class);

        Type::fromReflection($reflectionType);
    }

    #[Test]
    public function itCannotBuildFromReflectionIntersectionTypes(): void
    {
        $reflectionType = self::getReflectionOfReturnType(new class () {
            public function method(): Iterator&Countable
            {
                // fill in method;
            }
        }, 'method');

        self::expectException(RuntimeException::class);

        Type::fromReflection($reflectionType);
    }

    #[Test]
    #[DataProvider('provideReflectionNamedTypes')]
    public function ItBuildsFromReflectionNamedType(
        Type $expected,
        ReflectionNamedType $reflectionType,
    ): void {
        self::assertEquals($expected, Type::fromReflection($reflectionType));
    }

    /** @return Generator<array{ 0: Type, 1:ReflectionNamedType}> */
    public static function provideReflectionNamedTypes(): Generator
    {
        yield 'array' =>  [
            new Type('array', false),
            self::getReflectionOfReturnType(new class() {
                public function method(): array
                {
                    return [];
                }
            }, 'method'),
        ];

        yield 'nullable array' =>  [
            new Type('array', true),
            self::getReflectionOfReturnType(new class() {
                public function method(): ?array
                {
                    return [];
                }
            }, 'method'),
        ];

        yield 'bool' =>  [
            new Type('bool', false),
            self::getReflectionOfReturnType(new class() {
                public function method(): bool
                {
                    return true;
                }
            }, 'method'),
        ];

        yield 'nullable bool' =>  [
            new Type('bool', true),
            self::getReflectionOfReturnType(new class() {
                public function method(): ?bool
                {
                    return true;
                }
            }, 'method'),
        ];

        yield 'float' =>  [
            new Type('float', false),
            self::getReflectionOfReturnType(new class() {
                public function method(): float
                {
                    return 3.14;
                }
            }, 'method'),
        ];

        yield 'nullable float' =>  [
            new Type('float', true),
            self::getReflectionOfReturnType(new class() {
                public function method(): ?float
                {
                    return 3.14;
                }
            }, 'method'),
        ];

        yield 'int' =>  [
            new Type('int', false),
            self::getReflectionOfReturnType(new class() {
                public function method(): int
                {
                    return 1;
                }
            }, 'method'),
        ];

        yield 'nullable int' =>  [
            new Type('int', true),
            self::getReflectionOfReturnType(new class() {
                public function method(): ?int
                {
                    return 1;
                }
            }, 'method'),
        ];

        yield 'string' =>  [
            new Type('string', false),
            self::getReflectionOfReturnType(new class() {
                public function method(): string
                {
                    return '';
                }
            }, 'method'),
        ];

        yield 'nullable string' =>  [
            new Type('string', true),
            self::getReflectionOfReturnType(new class() {
                public function method(): ?string
                {
                    return '';
                }
            }, 'method'),
        ];

        yield Container::class => [
            new Type(Container::class, false),
            self::getReflectionOfReturnType(new class() {
                public function method(): Container
                {
                    return self::createStub(Container::class);
                }
            }, 'method'),
        ];

        yield sprintf('nullable %s', Container::class) => [
            new Type(Container::class, true),
            self::getReflectionOfReturnType(new class() {
                public function method(): ?Container
                {
                    return self::createStub(Container::class);
                }
            }, 'method'),
        ];

        yield Directory::class => [
            new Type(Directory::class, false),
            self::getReflectionOfReturnType(new class() {
                public function method(): Directory
                {
                    return self::createStub(Directory::class);
                }
            }, 'method'),
        ];

        yield sprintf('nullable %s', Directory::class) => [
            new Type(Directory::class, true),
            self::getReflectionOfReturnType(new class() {
                public function method(): ?Directory
                {
                    return self::createStub(Directory::class);
                }
            }, 'method'),
        ];

        yield File::class => [
            new Type(File::class, false),
            self::getReflectionOfReturnType(new class() {
                public function method(): File
                {
                    return self::createStub(File::class);
                }
            }, 'method'),
        ];

        yield sprintf('nullable %s', File::class) => [
            new Type(File::class, true),
            self::getReflectionOfReturnType(new class() {
                public function method(): ?File
                {
                    return self::createStub(File::class);
                }
            }, 'method'),
        ];
    }

    private static function getReflectionOfReturnType(
        object $class,
        string $method
    ): ReflectionType {
        return (new ReflectionMethod($class, $method))->getReturnType();
    }
}
