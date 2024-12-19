<?php

declare(strict_types=1);

namespace Dagger\ValueObject\TypeHint;

use Dagger\Client\AbstractScalar;
use Dagger\Client\IdAble;
use Dagger\Exception\UnsupportedType;
use Dagger\TypeDefKind;
use Dagger\ValueObject\TypeHint;
use DomainException;
use ReflectionClass;
use ReflectionNamedType;
use ReflectionType;

final readonly class Type implements TypeHint
{
    private TypeDefKind $typeDefKind;

    public function __construct(
        private string $name,
        private bool $nullable = false,
    ) {
        $this->typeDefKind = $this->determineTypeDefKind($name);
    }

    public function getName(): string
    {
        return $this->name;
    }

    public function getTypeDefKind(): TypeDefKind
    {
        return $this->typeDefKind;
    }

    public function isNullable(): bool
    {
        return $this->nullable;
    }

    public static function fromReflection(ReflectionType $type): self
    {
        if (!($type instanceof ReflectionNamedType)) {
            throw new UnsupportedType(sprintf(
                'Currently the PHP SDK only supports %s',
                ReflectionNamedType::class,
            ));
        }

        return new self($type->getName(), $type->allowsNull());
    }

    public function isIdable(): bool
    {
        if (!class_exists($this->name)) {
            return false;
        }

        $class = new ReflectionClass($this->name);

        return $class->implementsInterface(IdAble::class);
    }

    private function determineTypeDefKind(string $nameOfType): TypeDefKind
    {
        switch ($nameOfType) {
            case 'bool':
                return TypeDefKind::BOOLEAN_KIND;
            case 'int':
                return TypeDefKind::INTEGER_KIND;
            case 'string':
                return TypeDefKind::STRING_KIND;
            case 'null':
            case 'void':
                return TypeDefKind::VOID_KIND;
        }

        if (class_exists($nameOfType)) {
            $parents = class_parents($nameOfType);
            assert(is_array($parents));

            if (in_array(AbstractScalar::class, $parents, true)) {
                return TypeDefKind::SCALAR_KIND;
            }

            if (enum_exists($nameOfType)) {
                return TypeDefKind::ENUM_KIND;
            }

            return TypeDefKind::OBJECT_KIND;
        }

        if (interface_exists($nameOfType)) {
            return TypeDefKind::INTERFACE_KIND;
        }

        if ($nameOfType === 'array') {
            throw new DomainException(sprintf(
                '%s should not be used for arrays, use %s instead.',
                self::class,
                ListOfType::class,
            ));
        }

        throw new UnsupportedType(sprintf(
            'No matching "%s" for "%s"',
            TypeDefKind::class,
            $nameOfType,
        ));
    }
}
