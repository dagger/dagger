<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\ValueObject;

use Dagger\Exception\UnsupportedType;
use Dagger\Tests\Unit\Fixture;
use Dagger\Tests\Unit\Unsupported;
use Dagger\ValueObject;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(ValueObject\DaggerEnum::class)]
class DaggerEnumTest extends TestCase
{
    #[Test]
    #[DataProvider('provideStringBackedEnums')]
    public function itBuildsFromReflection(
        ValueObject\DaggerEnum $expected,
        \ReflectionEnum $reflection,
    ): void {
        $actual = ValueObject\DaggerEnum::fromReflection($reflection);
        self::assertEquals($expected, $actual);
    }

    /**
     * The backing value is registered as metadata; the case name is what
     * identifies a member to the engine. They must not be interchanged.
     */
    #[Test]
    public function itKeepsCaseNamesAndBackingValuesApart(): void
    {
        $actual = ValueObject\DaggerEnum::fromReflection(
            new \ReflectionEnum(Fixture\StringBackedEnum::class),
        );

        self::assertSame(
            ['Foo', 'Bar', 'Baz'],
            array_map(fn($c) => $c->name, $actual->cases),
        );
        self::assertSame(
            ['first nonsense word', 'second nonsense word', 'third nonsense word'],
            array_map(fn($c) => $c->value, $actual->cases),
        );
    }

    /**
     * A pure enum has no value to register. Leaving it empty keeps the engine
     * from emitting an @enumValue directive claiming one.
     */
    #[Test]
    public function itRegistersNoValueForPureEnumCases(): void
    {
        $actual = ValueObject\DaggerEnum::fromReflection(
            new \ReflectionEnum(Fixture\PureEnum::class),
        );

        self::assertSame(['Low', 'High'], array_map(fn($c) => $c->name, $actual->cases));
        self::assertSame(['', ''], array_map(fn($c) => $c->value, $actual->cases));
    }

    #[Test]
    #[DataProvider('provideUnsupportedEnums')]
    public function itRejectsUnsupportedEnums(
        string $enum,
        string $expectedMessage,
    ): void {
        self::expectException(UnsupportedType::class);
        self::expectExceptionMessage($expectedMessage);

        ValueObject\DaggerEnum::fromReflection(new \ReflectionEnum($enum));
    }

    #[Test]
    public function itRejectsEnumsMissingTheDaggerObjectAttribute(): void
    {
        self::expectException(\RuntimeException::class);
        self::expectExceptionMessage('class is not a DaggerObject');

        ValueObject\DaggerEnum::fromReflection(
            new \ReflectionEnum(Unsupported\UnattributedEnum::class),
        );
    }

    /**
     * @return \Generator<array{
     *     ValueObject\DaggerEnum,
     *     \ReflectionEnum,
     * }>
     */
    public static function provideStringBackedEnums(): \Generator
    {
        foreach ([
            'string-backed' => Fixture\StringBackedEnum::class,
            'int-backed' => Fixture\IntBackedEnum::class,
            'pure (no backing type)' => Fixture\PureEnum::class,
        ] as $label => $enum) {
            yield $label => [
                $enum::asValueObject(),
                new \ReflectionEnum($enum),
            ];
        }
    }

    /** @return \Generator<array{string, string}> */
    public static function provideUnsupportedEnums(): \Generator
    {
        yield 'enum without cases' => [
            Unsupported\CaselessBackedEnum::class,
            'declares none',
        ];
    }
}
