<?php

declare(strict_types=1);

namespace Dagger\Service\Serialisation;

use JMS\Serializer\EventDispatcher\EventSubscriberInterface;
use JMS\Serializer\EventDispatcher\PreDeserializeEvent;
use JMS\Serializer\EventDispatcher\PreSerializeEvent;

final readonly class EnumSubscriber implements EventSubscriberInterface
{
    public const ORIGINAL_CLASS =
        'The original class name before ' .
        'being changed to ' .
        \BackedEnum::class;

    public static function getSubscribedEvents(): array
    {
        return [
            [
                'event' => 'serializer.pre_serialize',
                'method' => 'onPreSerialize',
                'interface' => \UnitEnum::class,
            ],
            [
                'event' => 'serializer.pre_deserialize',
                'method' => 'onPreDeserialize',
            ],
        ];
    }

    public function onPreSerialize(PreSerializeEvent $event): void
    {
        if ($event->getObject() instanceof \UnitEnum) {
            $event->setType(\UnitEnum::class);
        }
    }

    public function onPreDeserialize(PreDeserializeEvent $event): void
    {
        $className = $event->getType()['name'];

        if (!enum_exists($className)) {
            return;
        }

        $event->setType(\UnitEnum::class, array_merge_recursive(
            $event->getType()['params'],
            [self::ORIGINAL_CLASS => $className]
        ));
    }
}
