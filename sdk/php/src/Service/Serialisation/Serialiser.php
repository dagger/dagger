<?php

declare(strict_types=1);

namespace Dagger\Service\Serialisation;

use Dagger\ValueObject\Argument;
use Dagger\ValueObject\ListOfType;
use Dagger\ValueObject\Type;
use Dagger\ValueObject\TypeHint;
use JMS\Serializer\DeserializationContext;
use JMS\Serializer\EventDispatcher\EventDispatcher;
use JMS\Serializer\EventDispatcher\PreDeserializeEvent;
use JMS\Serializer\EventDispatcher\PreSerializeEvent;
use JMS\Serializer\Handler\HandlerRegistry;
use JMS\Serializer\Handler\SubscribingHandlerInterface;
use JMS\Serializer\SerializationContext;
use JMS\Serializer\Serializer;
use JMS\Serializer\SerializerBuilder;
use RuntimeException;

final readonly class Serialiser
{
    private Serializer $serializer;

    /**
     * @param \JMS\Serializer\EventDispatcher\EventSubscriberInterface[] $subscribers
     * @param \JMS\Serializer\Handler\SubscribingHandlerInterface[] $handlers
     */
    public function __construct(array $subscribers = [], array $handlers = [])
    {
        $this->serializer = SerializerBuilder::create()
            ->configureListeners(
                function (EventDispatcher $dispatcher) use ($subscribers) {
                    foreach ($subscribers as $subscriber) {
                        $dispatcher->addSubscriber($subscriber);
                    }
                }
            )
            ->configureHandlers(
                function (HandlerRegistry $registry) use ($handlers) {
                    foreach ($handlers as $handler) {
                        $registry->registerSubscribingHandler($handler);
                    }
                }
            )
            ->addDefaultHandlers()
            ->build();
    }

    public function serialise(mixed $value): string
    {
        return $this->serializer->serialize(
            $value,
            'json',
            SerializationContext::create()->setSerializeNull(true),
        );
    }

    public function deserialise(string $value, TypeHint $typeHint): mixed
    {
        if ($value === 'null') {
            return $typeHint->nullable ? null :
                throw new RuntimeException('value cannot be null');
        }

        return $this->serializer
            ->deserialize($value, $typeHint->getName(), 'json');
    }
}
