<?php

declare(strict_types=1);

namespace Dagger\Module;

use Dagger\Attribute;
use Dagger\Client;
use Dagger\Client\IdAble;
use Dagger\Id;
use Dagger\Json;
use Dagger\Service\Serialisation;
use Dagger\TypeDefKind;
use Dagger\ValueObject\ListOfType;
use Dagger\ValueObject\Type;
use InvalidArgumentException;
use ReflectionClass;
use ReflectionNamedType;
use ReflectionProperty;
use stdClass;

/**
 * Decodes values that json_decode has already made structural.
 */
final readonly class ValueCodec
{
    public function __construct(
        private Serialisation\Serialiser $serialiser,
        private Client $client,
    ) {
    }

    public static function forClient(Client $client): self
    {
        return new self(new Serialisation\Serialiser(
            [
                new Serialisation\AbstractScalarSubscriber(),
                new Serialisation\IdableSubscriber(),
                new Serialisation\EnumSubscriber(),
            ],
            [
                new Serialisation\AbstractScalarHandler(),
                new Serialisation\IdableHandler($client),
                new Serialisation\EnumHandler(),
            ],
        ), $client);
    }

    public function decode(mixed $value, ListOfType|Type $type): mixed
    {
        if ($value === null) {
            return $type->nullable || $type->typeDefKind === TypeDefKind::VOID_KIND
                ? null
                : throw new InvalidArgumentException(
                    sprintf('null given for a value of the non-nullable type %s', self::typeName($type)),
                );
        }

        if ($type instanceof ListOfType) {
            if (!is_array($value) || !array_is_list($value)) {
                throw self::unexpected($value, $type);
            }

            return array_map(fn($element) => $this->decode($element, $type->subtype), $value);
        }

        $decoded = match ($type->typeDefKind) {
            TypeDefKind::BOOLEAN_KIND => is_bool($value) ? $value : throw self::unexpected($value, $type),
            TypeDefKind::INTEGER_KIND => is_int($value) ? $value : throw self::unexpected($value, $type),
            TypeDefKind::FLOAT_KIND => is_int($value) || is_float($value)
                ? (float) $value
                : throw self::unexpected($value, $type),
            TypeDefKind::STRING_KIND => is_string($value) ? $value : throw self::unexpected($value, $type),
            TypeDefKind::VOID_KIND => null,
            TypeDefKind::SCALAR_KIND => $this->decodeScalar($value, $type),
            TypeDefKind::ENUM_KIND => $this->serialiser->deserialise(
                json_encode($value, JSON_THROW_ON_ERROR),
                $type->name,
            ),
            TypeDefKind::OBJECT_KIND => $type->isIdable()
                ? $this->serialiser->deserialise(json_encode($value, JSON_THROW_ON_ERROR), $type->name)
                : $this->decodeObject($value, $type->name),
            TypeDefKind::INTERFACE_KIND => $type->isDaggerInterface()
                ? $this->decodeInterface($value, $type)
                : throw new InvalidArgumentException(
                    sprintf('Currently cannot decode custom interfaces: %s', $type->name),
                ),
            default => throw new InvalidArgumentException("Cannot decode $type->name"),
        };

        // EnumHandler reads an empty string as null.
        if ($decoded === null && !$type->nullable && $type->typeDefKind !== TypeDefKind::VOID_KIND) {
            throw self::unexpected($value, $type);
        }

        return $decoded;
    }

    public function encode(mixed $value, ListOfType|Type|null $type = null): string
    {
        return $this->serialiser->serialise(self::encodable($value, $type));
    }

    private function decodeScalar(mixed $value, Type $type): mixed
    {
        if (!is_string($value) && $type->name !== Json::class) {
            throw self::unexpected($value, $type);
        }

        return $this->serialiser->deserialise(
            json_encode(
                is_string($value) ? $value : json_encode($value, JSON_THROW_ON_ERROR),
                JSON_THROW_ON_ERROR,
            ),
            $type->name,
        );
    }

    private function decodeInterface(mixed $id, Type $type): object
    {
        if (!is_string($id)) {
            throw self::unexpected($id, $type);
        }

        $class = InterfaceClients::className($type->name);
        if (!class_exists($class)) {
            throw new InvalidArgumentException("No class $class exists; run dagger generate");
        }

        return $this->client->loadObjectFromId($class, new Id($id), constant("$class::GRAPHQL_TYPE"));
    }

    /**
     * The serialiser leaves an untyped array as it found it and knows nothing of
     * ListOfType, so a declared list and any Dagger object that may hold one are
     * decoded here. They are removed first because a readonly property can only
     * be initialised once.
     *
     * @param class-string $class
     */
    private function decodeObject(mixed $state, string $class): object
    {
        if (!is_array($state)) {
            throw self::unexpected($state, new Type($class));
        }

        $decoded = [];
        foreach ((new ReflectionClass($class))->getProperties() as $property) {
            $name = $property->getName();
            $type = array_key_exists($name, $state) ? self::structuralType($property) : null;
            if ($type === null) {
                continue;
            }

            $decoded[] = [$name, $property->getDeclaringClass()->getName(), $this->decode($state[$name], $type)];
            unset($state[$name]);
        }

        $object = $this->serialiser->deserialise(
            json_encode($state === [] ? new stdClass() : $state, JSON_THROW_ON_ERROR),
            $class,
        );

        foreach ($decoded as [$name, $declaringClass, $value]) {
            \Closure::bind(function () use ($name, $value): void {
                $this->$name = $value;
            }, $object, $declaringClass)();
        }

        return $object;
    }

    private static function structuralType(ReflectionProperty $property): ListOfType|Type|null
    {
        $type = $property->getType();
        if (!$type instanceof ReflectionNamedType) {
            return null;
        }

        if ($type->getName() === 'array') {
            $attribute = current($property->getAttributes(Attribute\ListOfType::class)) ?: null;

            return $attribute === null ? null : ListOfType::fromReflection($type, $attribute->newInstance());
        }

        return class_exists($type->getName())
            && (new ReflectionClass($type->getName()))->getAttributes(Attribute\DaggerObject::class) !== []
            ? new Type($type->getName(), $type->allowsNull())
            : null;
    }

    /**
     * The value as the engine can read it once serialised. A list that
     * array_filter or a keyed build has left with gaps would otherwise reach
     * the engine as a JSON object. The engine reads an interface value only as
     * the ID of an implementation it holds (dagger/dagger core/interface.go,
     * ConvertFromSDKResult), so a plain PHP implementation is refused.
     */
    private static function encodable(mixed $value, ListOfType|Type|null $type): mixed
    {
        if (
            $type instanceof Type
            && $type->typeDefKind === TypeDefKind::INTERFACE_KIND
            && $value !== null
            && !$value instanceof IdAble
        ) {
            throw new InvalidArgumentException(sprintf(
                '%s cannot be returned as the interface %s: return an implementation received as that interface',
                get_debug_type($value),
                $type->name,
            ));
        }

        if (!$type instanceof ListOfType || !is_array($value)) {
            return $value;
        }

        return array_map(
            static fn($element) => self::encodable($element, $type->subtype),
            array_values($value),
        );
    }

    private static function unexpected(mixed $value, ListOfType|Type $type): InvalidArgumentException
    {
        return new InvalidArgumentException(sprintf(
            '%s is not a value of the type %s',
            get_debug_type($value),
            self::typeName($type),
        ));
    }

    private static function typeName(ListOfType|Type $type): string
    {
        return $type instanceof ListOfType ? '[' . self::typeName($type->subtype) . ']' : $type->name;
    }
}
