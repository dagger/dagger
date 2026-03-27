<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Attribute;
use Dagger\Exception\RegistrationError\MissingAttribute;
use Dagger\Exception\UnsupportedType;
use ReflectionProperty;
use ReflectionNamedType;
use RuntimeException;

/** @internal Value Object used for fields to expose to Dagger. */
final readonly class DaggerField
{
    public function __construct(
        public string $name,
        public ?string $description,
        public ListOfType|Type $type,
    ) {
    }

    /**
     * @throws RuntimeException
     * - if not decorated by the DaggerFunction Attribute
     * - if missing a type hint
     *
     * @throws UnsupportedType
     * - for type hints that are not supported currently.
     */
    public static function fromReflection(ReflectionProperty $field): self
    {
        if ($field->getAttributes(Attribute\DaggerFunction::class) === []) {
            throw new RuntimeException(sprintf(
                'Field "%s" is not considered a dagger field without the %s attribute',
                $field->getName(),
                Attribute\DaggerFunction::class
            ));
        }

        $description = (current($field
            ->getAttributes(Attribute\Doc::class)) ?: null)
            ?->newInstance()
            ?->description;

        $type = self::getType($field);

        return new self($field->name, $description, $type);
    }

    private static function getType(ReflectionProperty $field): ListOfType|Type
    {
        $type = $field->getType() ?? throw new RuntimeException(sprintf(
            'DaggerField "%s" cannot be supported without a type',
            $field->name,
        ));

        if (!($type instanceof ReflectionNamedType)) {
            throw new UnsupportedType(
                'The PHP SDK only supports named types and nullable named types',
            );
        }

        if ($type->getName() === 'array') {
            $attribute = (current($field
                ->getAttributes(Attribute\ListOfType::class)) ?: null)
                ?->newInstance()
                ?? throw MissingAttribute::fieldListOfType($field->getName());

            return ListOfType::fromReflection($type, $attribute);
        }

        return Type::fromReflection($type);
    }
}
