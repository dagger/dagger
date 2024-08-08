<?php

declare(strict_types=1);

namespace Dagger\Service\Serialisation;

use Dagger\Client;
use Dagger\Client\AbstractScalar;
use Dagger\Client\IdAble;
use JMS\Serializer\Context;
use JMS\Serializer\GraphNavigatorInterface;
use JMS\Serializer\Handler\SubscribingHandlerInterface;
use JMS\Serializer\JsonDeserializationVisitor;
use JMS\Serializer\JsonSerializationVisitor;
use ReflectionClass;

final readonly class IdableHandler implements SubscribingHandlerInterface
{
    public function __construct(
        private Client $client,
    ) {
    }

    /**
     * @return array<array{
     *     direction: 1|2,
     *     format: string,
     *     type: string,
     *     method: string,
     * }>
     */
    public static function getSubscribingMethods(): array
    {
        return [
            [
                'direction' => GraphNavigatorInterface::DIRECTION_SERIALIZATION,
                'format' => 'json',
                'type' => IdAble::class,
                'method' => 'serialise',
            ],
            [
                'direction' => GraphNavigatorInterface::DIRECTION_DESERIALIZATION,
                'format' => 'json',
                'type' => IdAble::class,
                'method' => 'deserialise'
            ],
        ];
    }

    public function serialise(
        JsonSerializationVisitor $visitor,
        IdAble $idAble,
        array $type,
        Context $context
    ): string {
        return (string) $idAble->id();
    }

    public function deserialise(
        JsonDeserializationVisitor $visitor,
        string $idAble,
        array $type,
        Context $context,
    ): IdAble {
        $originalClassName = $type['params'][
            IdableSubscriber::ORIGINAL_CLASS_NAME
        ];

        $shortName = (new ReflectionClass($originalClassName))->getShortName();
        $method = sprintf('load%sFromId', $shortName);
        $id = sprintf('%sId', $originalClassName);

        return $this->client->$method(new $id($idAble));
    }
}
