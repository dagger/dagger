<?php

namespace Dagger\Tests\Unit\Service\Serialisation;

use Dagger\ContainerId;
use Dagger\Json;
use Dagger\NetworkProtocol;
use Dagger\Platform;
use Dagger\Service\Serialisation\AbstractScalarHandler;
use Dagger\Service\Serialisation\AbstractScalarSubscriber;
use Dagger\Service\Serialisation\EnumHandler;
use Dagger\Service\Serialisation\EnumSubscriber;
use Dagger\Service\Serialisation\Serialiser;
use Dagger\Tests\Unit\Fixture\Enum\StringBackedDummy;
use Dagger\TypeDefKind;
use Dagger\ValueObject\ListOfType;
use Dagger\ValueObject\Type;
use Generator;
use JMS\Serializer\EventDispatcher\EventSubscriberInterface;
use JMS\Serializer\Handler\SubscribingHandlerInterface;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(Serialiser::class)]
class SerialiserTest extends TestCase
{
    /**
     * @param EventSubscriberInterface[] $subscribers
     * @param SubscribingHandlerInterface[] $handlers
     */
    #[Test]
    #[DataProvider('provideEnums')]
    #[DataProvider('provideScalars')]
    #[DataProvider('provideListsOfScalars')]
//    #[DataProvider('provideAbstractScalars')]
    public function itSerialisesValues(
        array $subscribers,
        array $handlers,
        mixed $value,
        string $valueAsJSON,
    ): void {
        $sut = new Serialiser($subscribers, $handlers);

        self::assertSame($valueAsJSON, $sut->serialise($value));
    }

    /**
     * @param EventSubscriberInterface[] $subscribers
     * @param SubscribingHandlerInterface[] $handlers
     */
    #[Test]
    #[DataProvider('provideEnums')]
    #[DataProvider('provideScalars')]
    #[DataProvider('provideListsOfScalars')]
//    #[DataProvider('provideAbstractScalars')]
    public function itDeserialisesValues(
        array $subscribers,
        array $handlers,
        mixed $value,
        string $valueAsJSON,
        ListOfType|Type $type,
    ): void {
        $sut = new Serialiser($subscribers, $handlers);

        self::assertEquals($value, $sut->deserialise($valueAsJSON, $type));
    }

        /**
     * @return Generator<array{
     *     0: array{ 0:EnumSubscriber },
     *     1: array{ 0:EnumHandler },
     *     2: mixed,
     *     3: string,
     *     4: class-string,
     * }>
     */
    public static function provideEnums(): Generator
    {
        yield NetworkProtocol::class => [
            [new EnumSubscriber()],
            [new EnumHandler()],
            NetworkProtocol::TCP,
            '"TCP"',
            new Type(NetworkProtocol::class),
        ];

        yield StringBackedDummy::class => [
            [new EnumSubscriber()],
            [new EnumHandler()],
            StringBackedDummy::Hello,
            '"Hello"',
            new Type(StringBackedDummy::class),
        ];

        yield sprintf('nullable %s, receive null', NetworkProtocol::class) => [
            [new EnumSubscriber()],
            [new EnumHandler()],
            null,
            'null',
            new Type(NetworkProtocol::class, true),
        ];

        yield sprintf('list of %s', TypeDefKind::class) => (function () {
            $cases = TypeDefKind::cases();

            return [
                [new EnumSubscriber()],
                [new EnumHandler()],
                $cases,
                json_encode(array_map(fn($c) => $c->name, $cases)),
                new ListOfType(new Type(TypeDefKind::class)),
            ];
        })();
    }

    /**
     * @return Generator<array{
     *     0: array{},
     *     1: array{},
     *     2: mixed,
     *     3: string,
     *     4: string,
     * }>
     */
    public static function provideScalars(): Generator
    {
        foreach ([
            'bool true' => [true, 'true', new Type('bool')],
            'bool false' => [false, 'false', new Type('bool')],
            'nullable bool' => [null, 'null', new Type('bool', true)],

            'int' => [42, '42', new Type('int')],
            'nullable int' => [null, 'null', new Type('int', true)],

            'string' => ['Hello', '"Hello"', new Type('string')],
            'nullable string' => [null, 'null', new Type('string', true)],
        ] as $case => [$value, $valueAsJson, $type]) {
            yield $case => [[], [], $value, $valueAsJson, $type];
        }
    }

    /**
     * @return Generator<array{
     *     0: array{},
     *     1: array{},
     *     2: mixed,
     *     3: string,
     *     4: string,
     * }>
     */
    public static function provideListsOfScalars(): Generator
    {
        foreach ([
            'list of bools' => [
                [true, false],
                '[true,false]',
                new ListOfType(new Type('bool')),
            ],
            'empty list of bools' => [
                [],
                '[]',
                new ListOfType(new Type('bool')),
            ],
            'nullable list of bools' => [
                null,
                'null',
                new ListOfType(new Type('bool'), true),
            ],
            'list of nullable bools' => [
                [null, true, false, null],
                '[null,true,false,null]',
                new ListOfType(new Type('bool', true)),
            ],
            'list of lists of lists of bools' => [
                [[[true, false], [true]], [[false, true], [false]]],
                json_encode([[[true, false], [true]], [[false, true], [false]]]),
                new ListOfType(new ListOfType(new ListOfType(new Type('bool')))),
            ],

            'list of ints' => [
                [1, 2, 3],
                '[1,2,3]',
                new ListOfType(new Type('int')),
            ],
            'empty list of ints' => [
                [],
                '[]',
                new ListOfType(new Type('int')),
            ],
            'nullable list of ints' => [
                null,
                'null',
                new ListOfType(new Type('int'), true),
            ],
            'list of nullable ints' => [
                [1, null, 3],
                '[1,null,3]',
                new ListOfType(new Type('int', true)),
            ],
            'list of lists of lists of ints' => [
                [[[], [1]], [], [[2, 3], [4, 5, 6]], [[7, 8, 9]]],
                '[[[],[1]],[],[[2,3],[4,5,6]],[[7,8,9]]]',
                new ListOfType(new ListOfType(new ListOfType(new Type('int')))),
            ]
        ] as $case => [$value, $valueAsJson, $type]) {
            yield $case => [[], [], $value, $valueAsJson, $type];
        }
    }

    /**
     * @return Generator<array{
     *     0: array{ 0:AbstractScalarSubscriber },
     *     1: array{ 0:AbstractScalarHandler },
     *     2: mixed,
     *     3: string,
     *     4: string,
     * }>
     */
    public static function provideAbstractScalars(): Generator
    {
        $cases = [
            [new Platform('linux/amd64'), '"linux\/amd64"'],
            [new Json('{"bool_field":true}'), '"{\"bool_field\":true}"'],
            [new ContainerId('1234-567-89'), '"1234-567-89"'],
        ];

        foreach ($cases as [$value, $valueAsJson]) {
            yield $value::class => [
                [new AbstractScalarSubscriber()],
                [new AbstractScalarHandler()],
                $value,
                $valueAsJson,
                $value::class,
            ];
        }
    }
}
