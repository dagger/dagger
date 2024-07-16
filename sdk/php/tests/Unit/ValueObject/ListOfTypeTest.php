<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\ValueObject;

use Closure;
use Countable;
use Dagger;
use Dagger\Attribute;
use Dagger\Container;
use Dagger\File;
use Dagger\ListTypeDef;
use Dagger\TypeDefKind;
use Dagger\ValueObject\ListOfType;
use Dagger\ValueObject\Type;
use DateTimeImmutable;
use Generator;
use Iterator;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionClass;
use ReflectionFunction;
use ReflectionNamedType;
use ReflectionType;

#[Group('unit')]
#[CoversClass(ListOfType::class)]
class ListOfTypeTest extends TestCase
{
    #[Test]
    #[DataProvider('provideUnsupportedReflectionTypes')]
    public function itOnlyBuildsFromReflectionNamedType(
        ReflectionType $unsupportedReflectionType
    ): void {
        self::expectException(Dagger\Exception\UnsupportedType::class);

        ListOfType::fromReflection(
            $unsupportedReflectionType,
            new Attribute\ListOfType('string'),
        );
    }

    #[Test]
    #[DataProvider('provideUnsupportedReflectionNamedTypes')]
    public function itOnlyBuildsFromArrayReflections(
        ReflectionNamedType $reflectionType,
    ): void {
        self::expectException(\DomainException::class);

        ListOfType::fromReflection(
            $reflectionType,
            new Attribute\ListOfType('string'),
        );
    }

    #[Test]
    #[DataProvider('provideArrayReflections')]
    public function itBuildsFromArrayReflections(
        ListOfType $expected,
        ReflectionNamedType $reflection,
        Attribute\ListOfType $attribute,
    ): void {
        $actual = ListOfType::fromReflection($reflection, $attribute);

        self::assertEquals($expected, $actual);
    }

    #[Test]
    public function itIsListTypeDefKind(): void {
        self::assertEquals(
            TypeDefKind::LIST_KIND,
            (new ListOfType(new Type('string')))->typeDefKind
        );
    }

    /** @return Generator<array{0:ReflectionType}> */
    public static function provideUnsupportedReflectionTypes(): Generator
    {
        yield 'union type' => [
            (new ReflectionFunction(function(): Iterator&Countable {}))
                ->getReturnType(),
        ];

        yield 'intersection type' => [
            (new ReflectionFunction(function(): Iterator|Countable {}))
                ->getReturnType(),
        ];

        yield 'custom reflection type' => [
            (new class () extends ReflectionType {}),
        ];
    }

    /** @return Generator<array{ 0: Type, 1:ReflectionNamedType}> */
    public static function provideUnsupportedReflectionNamedTypes(): Generator
    {
        $reflectReturnType = fn(Closure $fn) => (
            new ReflectionFunction($fn)
        )->getReturnType();

        yield 'bool' =>  [$reflectReturnType(fn(): bool => true)];

        yield 'nullable bool' =>  [$reflectReturnType(fn(): ?bool => false)];

        yield 'int' =>  [$reflectReturnType(fn(): int => 1)];

        yield 'nullable int' =>  [$reflectReturnType(fn(): ?int => 1)];

        yield 'string' =>  [$reflectReturnType(fn(): string => '')];

        yield 'nullable string' =>  [$reflectReturnType(fn(): ?string => '')];

        yield Dagger\Container::class => [
            $reflectReturnType(fn(): Dagger\Container => self
                ::createStub(Dagger\Container::class)),
        ];

        yield sprintf('nullable %s', Dagger\Container::class) => [
            $reflectReturnType(fn(): ?Dagger\Container => null),
        ];

        yield Dagger\Directory::class => [
            $reflectReturnType(fn(): Dagger\Directory => self
                ::createStub(Dagger\Directory::class)),
        ];

        yield sprintf('nullable %s', Dagger\Directory::class) => [
            $reflectReturnType(fn(): ?Dagger\Directory => null),
        ];

        yield Dagger\File::class => [
            $reflectReturnType(fn(): Dagger\File => self
                ::createStub(Dagger\File::class)),
        ];

        yield sprintf('nullable %s', Dagger\File::class) => [
            $reflectReturnType(fn(): ?Dagger\File => null),
        ];
    }

    public static function provideArrayReflections(): Generator
    {
        $arrayReflection = (new ReflectionFunction(fn(): array => []))
            ->getReturnType();

        $nullableArrayReflection = (new ReflectionFunction(fn(): ?array => []))
            ->getReturnType();

        yield '[String]' => [
            new ListOfType(new Type('string', true), true),
            $nullableArrayReflection,
            new Attribute\ListOfType('string', true),
        ];

        yield '[String]!' => [
            new ListOfType(new Type('string', true), false),
            $arrayReflection,
            new Attribute\ListOfType('string', true),
        ];

        yield '[String!]' => [
            new ListOfType(new Type('string', false), true),
            $nullableArrayReflection,
            new Attribute\ListOfType('string', false),
        ];

        yield '[String!]!' => [
            new ListOfType(new Type('string', false), false),
            $arrayReflection,
            new Attribute\ListOfType('string', false),
        ];

        yield '[[[String]]]' => [
            new ListOfType(
                new ListOfType(
                    new ListOfType(new Type('string', true), true),
                    true,
                ),
                true
            ),
            $nullableArrayReflection,
            new Attribute\ListOfType(
                new Attribute\ListOfType(
                    new Attribute\ListOfType('string', true),
                    true
                ),
                true,
            ),
        ];

        yield '[[[String!]!]!]!' => [
            new ListOfType(
                new ListOfType(
                    new ListOfType(new Type('string', false), false),
                    false,
                ),
                false
            ),
            $arrayReflection,
            new Attribute\ListOfType(
                new Attribute\ListOfType(
                    new Attribute\ListOfType('string', false),
                    false
                ),
                false,
            ),
        ];

        yield '[Int]' => [
            new ListOfType(new Type('int', true), true),
            $nullableArrayReflection,
            new Attribute\ListOfType('int', true),
        ];

        yield '[Int]!' => [
            new ListOfType(new Type('int', true), false),
            $arrayReflection,
            new Attribute\ListOfType('int', true),
        ];

        yield '[Int!]' => [
            new ListOfType(new Type('int', false), true),
            $nullableArrayReflection,
            new Attribute\ListOfType('int', false),
        ];

        yield '[Int!]!' => [
            new ListOfType(new Type('int', false), false),
            $arrayReflection,
            new Attribute\ListOfType('int', false),
        ];

        yield '[[[Int]]]' => [
            new ListOfType(
                new ListOfType(
                    new ListOfType(new Type('int', true), true),
                    true,
                ),
                true
            ),
            $nullableArrayReflection,
            new Attribute\ListOfType(
                new Attribute\ListOfType(
                    new Attribute\ListOfType('int', true),
                    true
                ),
                true,
            ),
        ];

        yield '[[[Int!]!]!]!' => [
            new ListOfType(
                new ListOfType(
                    new ListOfType(new Type('int', false), false),
                    false,
                ),
                false
            ),
            $arrayReflection,
            new Attribute\ListOfType(
                new Attribute\ListOfType(
                    new Attribute\ListOfType('int', false),
                    false
                ),
                false,
            ),
        ];

        yield '[Boolean]' => [
            new ListOfType(new Type('bool', true), true),
            $nullableArrayReflection,
            new Attribute\ListOfType('bool', true),
        ];

        yield '[Boolean]!' => [
            new ListOfType(new Type('bool', true), false),
            $arrayReflection,
            new Attribute\ListOfType('bool', true),
        ];

        yield '[Boolean!]' => [
            new ListOfType(new Type('bool', false), true),
            $nullableArrayReflection,
            new Attribute\ListOfType('bool', false),
        ];

        yield '[Boolean!]!' => [
            new ListOfType(new Type('bool', false), false),
            $arrayReflection,
            new Attribute\ListOfType('bool', false),
        ];

        yield '[[[Boolean]]]' => [
            new ListOfType(
                new ListOfType(
                    new ListOfType(new Type('bool', true), true),
                    true,
                ),
                true
            ),
            $nullableArrayReflection,
            new Attribute\ListOfType(
                new Attribute\ListOfType(
                    new Attribute\ListOfType('bool', true),
                    true
                ),
                true,
            ),
        ];

        yield '[[[Boolean!]!]!]!' => [
            new ListOfType(
                new ListOfType(
                    new ListOfType(new Type('bool', false), false),
                    false,
                ),
                false
            ),
            $arrayReflection,
            new Attribute\ListOfType(
                new Attribute\ListOfType(
                    new Attribute\ListOfType('bool', false),
                    false
                ),
                false,
            ),
        ];

        yield '[Container]' => [
            new ListOfType(new Type(Container::class, true), true),
            $nullableArrayReflection,
            new Attribute\ListOfType(Container::class, true),
        ];

        yield '[File]!' => [
            new ListOfType(new Type(File::class, true), false),
            $arrayReflection,
            new Attribute\ListOfType(File::class, true),
        ];
    }
}
