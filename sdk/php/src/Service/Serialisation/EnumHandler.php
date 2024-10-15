<?php

declare(strict_types=1);

namespace Dagger\Service\Serialisation;

use JMS\Serializer\Context;
use JMS\Serializer\GraphNavigatorInterface;
use JMS\Serializer\Handler\SubscribingHandlerInterface;
use JMS\Serializer\JsonDeserializationVisitor;
use JMS\Serializer\JsonSerializationVisitor;
use ReflectionEnum;
use ReflectionException;
use UnitEnum;

final readonly class EnumHandler implements SubscribingHandlerInterface
{
    /**
     * @return array<array{
     *     direction: 1|2,
     *     format: string,
     *     type: class-string,
     *     method: string,
     * }>
     */
    public static function getSubscribingMethods(): array
    {
        return [[
            'direction' => GraphNavigatorInterface::DIRECTION_SERIALIZATION,
            'format' => 'json',
            'type' => UnitEnum::class,
            'method' => 'serialise',
        ], [
            'direction' => GraphNavigatorInterface::DIRECTION_DESERIALIZATION,
            'format' => 'json',
            'type' => UnitEnum::class,
            'method' => 'deserialise'
        ]];
    }

    public function serialise(
        JsonSerializationVisitor $visitor,
        ?UnitEnum $enum,
        array $type,
        Context $context,
    ): ?string {
        return $enum?->name;
    }

    public function deserialise(
        JsonDeserializationVisitor $visitor,
        ?string $name,
        array $type,
        Context $context,
    ): ?UnitEnum {
        if ($name === null) {
            return null;
        }

        $class = $type['params'][EnumSubscriber::ORIGINAL_CLASS] ??
            throw new \RuntimeException(
                'Cannot find original class name.' .
                ' If this issue occurs, it is a bug',
            );

        if (!in_array(UnitEnum::class, class_implements($class) ?: [])) {
            throw new \RuntimeException(
                "'$class' was expected to be an enum." .
                ' If this issue occurs, it is a bug',
            );
        }

        $reflection = new ReflectionEnum($class);
        try {
            return $reflection->getCase($name)->getValue();
        } catch (ReflectionException) {
            throw new \RuntimeException(sprintf(
                "'$name' is not case of '$class', available cases are: '%s'",
                implode('\', \'', array_map(fn($c) => $c->name, $reflection
                    ->getCases()))
            ));
        }
    }
}
