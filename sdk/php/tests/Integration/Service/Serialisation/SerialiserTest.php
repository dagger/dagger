<?php

namespace Dagger\Tests\Integration\Service\Serialisation;

use Dagger\Client;
use Dagger\ContainerId;
use Dagger\Dagger;
use Dagger\Directory;
use Dagger\File;
use Dagger\Json;
use Dagger\NetworkProtocol;
use Dagger\Platform;
use Dagger\Service\Serialisation\AbstractScalarHandler;
use Dagger\Service\Serialisation\AbstractScalarSubscriber;
use Dagger\Service\Serialisation\EnumHandler;
use Dagger\Service\Serialisation\EnumSubscriber;
use Dagger\Service\Serialisation\IdableHandler;
use Dagger\Service\Serialisation\IdableSubscriber;
use Dagger\Service\Serialisation\Serialiser;
use Dagger\Tests\Unit\Fixture\DaggerObject\HandlingEnums;
use Dagger\Tests\Unit\Fixture\Enum\StringBackedDummy;
use Dagger\ValueObject\Type;
use Generator;
use JMS\Serializer\EventDispatcher\EventSubscriberInterface;
use JMS\Serializer\Handler\SubscribingHandlerInterface;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

use function Dagger\dag;

#[Group('integration')]
#[CoversClass(Serialiser::class)]
class SerialiserTest extends TestCase
{
    #[Test]
    #[DataProvider('provideIdables')]
    public function itSerialisesIdables(Client\IdAble $idable): void
    {
        $encodedId = json_encode($idable->id()->getValue());

        $sut = new Serialiser(
            [new IdableSubscriber()],
            [new IdableHandler(dag())]
        );

        self::assertSame($encodedId, $sut->serialise($idable), 'id does not match');
    }

    #[Test]
    #[DataProvider('provideIdables')]
    public function itDeserialisesIdables(Client\IdAble $idable): void
    {
        $encodedId = json_encode($idable->id()->getValue());
        $type = get_class($idable);

        $sut = new Serialiser(
            [new IdableSubscriber()],
            [new IdableHandler(dag())]
        );

        $actual = $sut->deserialise($encodedId, new Type($type));

        self::assertInstanceOf($type, $actual, "did not deserialise to $type");
        self::assertEquals($idable->id(), $actual->id(), 'id does not match');
    }

        /**
     * @return Generator<array{
     *     0: array{ 0:IdableSubscriber },
     *     1: array{ 0:IdableHandler },
     *     2: mixed,
     *     3: string,
     *     4: string,
     * }>
     */
    public static function provideIdables(): Generator
    {
        yield Directory::class => [dag()->directory()];
        yield File::class => [dag()
            ->container()
            ->withNewFile('/tmp/test', '')
            ->file('/tmp/test')];
    }
}
