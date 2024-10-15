<?php

declare(strict_types=1);

namespace Dagger\Service\Serialisation;

use Dagger\Client\IdAble;
use JMS\Serializer\EventDispatcher\EventSubscriberInterface;
use JMS\Serializer\EventDispatcher\PreDeserializeEvent;
use JMS\Serializer\EventDispatcher\PreSerializeEvent;

final readonly class IdableSubscriber implements EventSubscriberInterface
{
    public const ORIGINAL_CLASS_NAME =
        'The original class name before ' .
        'being changed to ' .
        IdAble::class;

    public static function getSubscribedEvents(): array
    {
        return [
            [
                'event' => 'serializer.pre_serialize',
                'method' => 'onPreSerialize',
                'interface' => IdAble::class,
            ],
            [
                'event' => 'serializer.pre_deserialize',
                'method' => 'onPreDeserialize',
            ],
        ];
    }

    public function onPreSerialize(PreSerializeEvent $event): void
    {
        if ($event->getObject() instanceof IdAble) {
            $event->setType(IdAble::class);
        }
    }

    public function onPreDeserialize(PreDeserializeEvent $event): void
    {
        $className = $event->getType()['name'];

        if (
            !class_exists($className)
            || !in_array(IdAble::class, class_implements($className))
        ) {
            return;
        }

        $event->setType(IdAble::class, array_merge_recursive(
            $event->getType()['params'],
            [self::ORIGINAL_CLASS_NAME => $className]
        ));
    }
}
