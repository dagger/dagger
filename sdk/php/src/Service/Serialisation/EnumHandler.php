<?php

declare(strict_types=1);

namespace Dagger\Service\Serialisation;

use Dagger\Exception\SDKBug;
use JMS\Serializer\Context;
use JMS\Serializer\GraphNavigatorInterface;
use JMS\Serializer\Handler\SubscribingHandlerInterface;
use JMS\Serializer\JsonDeserializationVisitor;
use JMS\Serializer\JsonSerializationVisitor;
use ReflectionEnum;
use ReflectionException;
use BackedEnum;

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
            'type' => BackedEnum::class,
            'method' => 'serialise',
        ], [
            'direction' => GraphNavigatorInterface::DIRECTION_DESERIALIZATION,
            'format' => 'json',
            'type' => BackedEnum::class,
            'method' => 'deserialise'
        ]];
    }

    public function serialise(
        JsonSerializationVisitor $visitor,
        ?BackedEnum $enum,
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
    ): ?BackedEnum {
        if ($name === null) {
            return null;
        }

        $class = $type['params'][EnumSubscriber::ORIGINAL_CLASS]
            ?? throw new SDKBug('Cannot determine enum class name.');

        if (!in_array(BackedEnum::class, class_implements($class) ?: [])) {
            throw new SDKBug("'$class' was expected to be an enum.");
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
