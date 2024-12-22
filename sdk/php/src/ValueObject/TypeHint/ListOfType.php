<?php

declare(strict_types=1);

namespace Dagger\ValueObject\TypeHint;

use Dagger\Attribute;
use Dagger\Exception\UnsupportedType;
use Dagger\TypeDefKind;
use Dagger\ValueObject\TypeHint;
use ReflectionNamedType;
use ReflectionType;

final readonly class ListOfType implements TypeHint
{
    private TypeDefKind $typeDefKind;

    public function __construct(
        private TypeHint $subtype,
        private bool $nullable = false,
    ) {
        $this->typeDefKind = TypeDefKind::LIST_KIND;
    }

    public function getName(): string
    {
        return sprintf('array<%s>', $this->subtype->getName());
    }

    public function getTypeDefKind(): TypeDefKind
    {
        return TypeDefKind::LIST_KIND;
    }

    public function isNullable(): bool
    {
        return $this->nullable;
    }

    public function getSubtype(): TypeHint
    {
        return $this->subtype;
    }

    public static function fromReflection(
        ReflectionType $type,
        Attribute\ListOfType|Attribute\ReturnsListOfType $attribute,
    ): self {
        if (!($type instanceof ReflectionNamedType)) {
            throw new UnsupportedType(sprintf(
                'Currently the PHP SDK only supports %s',
                ReflectionNamedType::class,
            ));
        }

        if ($type->getName() !== 'array') {
            throw new \DomainException(sprintf(
                '%s should only be used for arrays. ' .
                ' If this error occurred outside of developing the PHP SDK, it is a bug.',
                self::class,
            ));
        }

        if (is_string($attribute->type)) {
            return new self(
                new Type($attribute->type, $attribute->nullable),
                $type->allowsNull(),
            );
        }

        return new self(self::determineSubtype($attribute), $type->allowsNull());
    }

    private static function determineSubtype(
        Attribute\ListOfType $attribute,
    ): TypeHint {
        if (is_string($attribute->type)) {
            return new Type($attribute->type, $attribute->nullable);
        }

        return new ListOfType(
            self::determineSubtype($attribute->type),
            $attribute->nullable,
        );
    }
}
