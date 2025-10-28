<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Attribute;
use Dagger\Client\IdAble;
use Dagger\Exception\RegistrationError\MissingAttribute;
use Dagger\Exception\UnsupportedType;
use Dagger\Json;
use ReflectionNamedType;
use ReflectionParameter;
use Roave\BetterReflection\Reflection\ReflectionParameter as BetterReflectionParameter;
use RuntimeException;

final readonly class Argument
{
    public function __construct(
        public string $name,
        public string $description,
        public ListOfType|Type $type,
        public ?Json $default = null,
        public ?string $defaultPath = null,
        public ?array $ignore = null,
    ) {
        if (!$type->nullable && $this->default == new Json('null')) {
            throw new RuntimeException(sprintf(
                'Non-nullable argument "%s" should not default to null.' .
                ' This error should only occur if constructed manually.' .
                ' Otherwise, it is a bug.',
                $this->name,
            ));
        }
    }

    public static function fromReflection(ReflectionParameter $parameter): self
    {
        $type = $parameter->getType() ?? throw new RuntimeException(sprintf(
            'Argument "%s" cannot be supported without a typehint',
            $parameter->name,
        ));

        $description = (current($parameter
            ->getAttributes(Attribute\Doc::class)) ?: null)
            ?->newInstance()
            ?->description;

        $defaultPathAttribute = (current($parameter
            ->getAttributes(Attribute\DefaultPath::class)) ?: null)
            ?->newInstance();

        $ignoreAttribute = (current($parameter
            ->getAttributes(Attribute\Ignore::class)) ?: null)
            ?->newInstance();

        return new self(
            name: $parameter->name,
            description: $description ?? '',
            type: self::getType($parameter),
            default: self::getDefault($parameter),
            defaultPath: $defaultPathAttribute?->path,
            ignore: $ignoreAttribute?->ignore,
        );
    }

    private static function getType(ReflectionParameter $parameter): ListOfType | Type
    {
        $type = $parameter->getType();

        if (! $type instanceof ReflectionNamedType) {
            throw new UnsupportedType(
                'The PHP SDK only supports named types and nullable named types',
            );
        }

        if ($type->getName() === 'array') {
            $attribute = (current($parameter
                ->getAttributes(Attribute\ListOfType::class)) ?: null)
                ?->newInstance()
                ?? throw MissingAttribute::listOfType(
                    $parameter->getDeclaringFunction()->getName(),
                    $parameter->getName(),
                );

            return ListOfType::fromReflection($type, $attribute);
        }

        return Type::fromReflection($type);
    }

    private static function getDefault(ReflectionParameter $parameter): ?Json
    {
        if ($parameter->isDefaultValueAvailable()) {
            $default = $parameter->getDefaultValue();
            return new Json(json_encode(
                $default instanceof IdAble ? (string) $default->id() : $default
            ));
        }

        if ($parameter->allowsNull()) {
            return new Json(json_encode(null));
        }

        return null;
    }
}
