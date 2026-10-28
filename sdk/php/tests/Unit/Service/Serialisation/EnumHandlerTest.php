<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Service\Serialisation;

use Dagger\Service\Serialisation\EnumHandler;
use Dagger\Service\Serialisation\EnumSubscriber;
use Dagger\Service\Serialisation\Serialiser;
use Dagger\Tests\Unit\Fixture\StringBackedEnum;
use Dagger\Tests\Unit\Fixture\EnumHolder;
use Dagger\Tests\Unit\Fixture\IntBackedEnum;
use Dagger\Tests\Unit\Fixture\PureEnum;
use Generator;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(EnumHandler::class)]
#[CoversClass(EnumSubscriber::class)]
class EnumHandlerTest extends TestCase
{
    private function sut(): Serialiser
    {
        return new Serialiser([new EnumSubscriber()], [new EnumHandler()]);
    }

    /**
     * The engine identifies a member by its case name. StringBackedEnum's
     * backing values differ from its case names, so these assertions fail if
     * the two are ever swapped.
     */
    #[Test, DataProvider('provideCases')]
    public function itSerialisesTheCaseNameNotTheBackingValue(
        StringBackedEnum $case,
    ): void {
        self::assertSame(
            sprintf('"%s"', $case->name),
            $this->sut()->serialise($case),
        );
    }

    #[Test, DataProvider('provideCases')]
    public function itDeserialisesByCaseName(StringBackedEnum $case): void
    {
        self::assertSame(
            $case,
            $this->sut()->deserialise(
                sprintf('"%s"', $case->name),
                StringBackedEnum::class,
            ),
        );
    }

    #[Test, DataProvider('provideCases')]
    public function itRefusesToDeserialiseTheBackingValue(
        StringBackedEnum $case,
    ): void {
        self::expectException(\RuntimeException::class);
        self::expectExceptionMessage('available cases are');

        $this->sut()->deserialise(
            sprintf('"%s"', $case->value),
            StringBackedEnum::class,
        );
    }

    #[Test]
    public function itReportsUnknownCasesHelpfully(): void
    {
        self::expectException(\RuntimeException::class);
        self::expectExceptionMessage("'Foo', 'Bar', 'Baz'");

        $this->sut()->deserialise('"Nope"', StringBackedEnum::class);
    }

    #[Test]
    public function itDeserialisesNullAsNull(): void
    {
        self::assertNull(
            $this->sut()->deserialise('null', StringBackedEnum::class),
        );
    }

    /**
     * Identity is the case name, so the backing type is irrelevant to the
     * wire format - a pure enum has no value to send in the first place.
     */
    #[Test, DataProvider('provideEveryEnumKind')]
    public function itRoundTripsEveryEnumKind(\UnitEnum $case): void
    {
        $sut = $this->sut();
        $json = $sut->serialise($case);

        self::assertSame(sprintf('"%s"', $case->name), $json);
        self::assertSame($case, $sut->deserialise($json, $case::class));
    }

    /**
     * EntrypointCommand serialises the parent object between calls, so in
     * practice enums travel nested inside another object rather than alone.
     */
    #[Test]
    public function itRoundTripsEnumsNestedInAnObject(): void
    {
        $sut = $this->sut();

        $holder = new EnumHolder();
        $holder->word = StringBackedEnum::Bar;
        $holder->pure = PureEnum::High;

        $json = $sut->serialise($holder);
        self::assertSame('{"word":"Bar","maybe":null,"pure":"High"}', $json);

        $actual = $sut->deserialise($json, EnumHolder::class);
        self::assertSame(StringBackedEnum::Bar, $actual->word);
        self::assertNull($actual->maybe);
        self::assertSame(PureEnum::High, $actual->pure);
    }

    /** @return Generator<array{\UnitEnum}> */
    public static function provideEveryEnumKind(): Generator
    {
        yield 'string-backed' => [StringBackedEnum::Foo];
        yield 'int-backed' => [IntBackedEnum::High];
        yield 'pure (no backing type)' => [PureEnum::Low];
    }

    /** @return Generator<array{StringBackedEnum}> */
    public static function provideCases(): Generator
    {
        foreach (StringBackedEnum::cases() as $case) {
            yield $case->name => [$case];
        }
    }
}
