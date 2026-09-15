<?php

namespace Dagger\tests\Unit\Service\Serialisation;

use Dagger\Client;
use Dagger\ContainerId;
use Dagger\Json;
use Dagger\Platform;
use Dagger\Service\Serialisation\AbstractScalarHandler;
use Dagger\Service\Serialisation\AbstractScalarSubscriber;
use Dagger\Service\Serialisation\Serialiser;
use Generator;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(Serialiser::class)]
class SerialiserTest extends TestCase
{
    #[Test, DataProvider('provideScalars')]
    public function itSerialisesScalars(mixed $value, string $valueAsJSON): void
    {
        self::assertSame($valueAsJSON, (new Serialiser())->serialise($value));
    }

    #[Test, DataProvider('provideScalars')]
    public function itDeserialisesScalars(mixed $value, string $valueAsJSON): void
    {
        $sut = new Serialiser();

        self::assertEquals($value, $sut->deserialise($valueAsJSON, gettype($value)));
    }

    #[Test, DataProvider('provideLists')]
    public function itSerialisesLists(?array $value, string $valueAsJSON): void
    {
        self::assertSame($valueAsJSON, (new Serialiser())->serialise($value));
    }

    #[Test, DataProvider('provideLists')]
    public function itDeserialisesLists(?array $value, string $valueAsJSON): void
    {
        $sut = new Serialiser();

        self::assertEquals($value, $sut->deserialise($valueAsJSON, 'array'));
    }

    #[Test, DataProvider('provideAbstractScalars')]
    public function itSerialisesAbstractScalars(
        Client\AbstractScalar $value,
        string $valueAsJSON,
    ): void {
        $sut = new Serialiser(
            [new AbstractScalarSubscriber()],
            [new AbstractScalarHandler()]
        );

        self::assertSame($valueAsJSON, $sut->serialise($value));
    }

    #[Test, DataProvider('provideAbstractScalars')]
    public function itDeserialisesAbstractScalars(
        Client\AbstractScalar $value,
        string $valueAsJSON,
    ): void {
        $sut = new Serialiser(
            [new AbstractScalarSubscriber()],
            [new AbstractScalarHandler()]
        );

        self::assertEquals($value, $sut->deserialise($valueAsJSON, $value::class));
    }

    /** @return Generator<array{ 0: mixed, 1: string }> */
    public static function provideScalars(): Generator
    {
        $cases = [
            true,
            false,
            123,
            null,
            'expected',
            'null',
        ];

        foreach ($cases as $case) {
            $type = gettype($case);
            yield "($type) $case" => [$case, json_encode($case)];
        }
    }

    /** @return Generator<array{ 0: ?array, 1: string }> */
    public static function provideLists(): Generator
    {
        yield 'string[]' => [['hello', 'world'], '["hello","world"]'];

        yield 'null' => [null, 'null'];

        yield ' null[]' => [[null, null], '[null,null]'];
    }

    /**
     * @return \Generator<array{
     *     0: \Dagger\Client\AbstractScalar|null,
     *     1: string,
     * }>
     */
    public static function provideAbstractScalars(): Generator
    {
        yield Platform::class => [
            new Platform('linux/amd64'),
            '"linux\/amd64"',
        ];

        yield Json::class => [
            new Json('{"bool_field":true}'),
            '"{\"bool_field\":true}"',
        ];

        yield ContainerId::class => [
            new ContainerId('1234-567-89'),
            '"1234-567-89"',
        ];
    }
}
