<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Client\AbstractScalar;
use Dagger\Client\IdAble;
use Dagger\Exception\UnsupportedType;
use Dagger\TypeDefKind;
use DomainException;
use ReflectionClass;
use ReflectionEnum;
use ReflectionNamedType;
use ReflectionType;
use RuntimeException;

final readonly class Type implements TypeHint
{
    public TypeDefKind $typeDefKind;

    public function __construct(
        public string $name,
        public bool $nullable = false,
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

    public function isIdable(): bool
    {
        if (!class_exists($this->name)) {
            return false;
        }

        $class = new ReflectionClass($this->name);

        return $class->implementsInterface(IdAble::class);
    }

    public function getNormalisedName(): string
    {
        if (!class_exists($this->name)) {
            throw new RuntimeException(sprintf(
                'cannot get normalised class name from type: %s',
                $this->name,
            ));
        }

        $result = explode('\\', $this->name);
        array_shift($result);
        return implode('\\', $result);
    }

    public function getShortName(): string
    {
        if (!class_exists($this->name)) {
            throw new RuntimeException(sprintf(
                'cannot get short class name from type: %s',
                $this->name,
            ));
        }

        $class = new ReflectionClass($this->name);

        return $class->getShortName();
    }

    /** @return \UnitEnum[] */
    public function getEnumCases(): array
    {
        if (!enum_exists($this->getName())) {
            throw new RuntimeException(sprintf(
                '%s is not an enum',
                $this->getName()
            ));
        }

        $reflection = new ReflectionEnum($this->getName());
        return array_map(fn($c) => $c->getValue(), $reflection->getCases());
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

    private function determineTypeDefKind(string $nameOfType): TypeDefKind
    {
        if ($nameOfType === 'array') {
            throw new DomainException(sprintf(
                '%s should not be constructed for arrays, use %s instead.' .
                ' If this error occurs, it is a bug.',
                self::class,
                ListOfType::class,
            ));
        }

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

        throw new UnsupportedType(sprintf(
            'No matching "%s" for "%s"',
            TypeDefKind::class,
            $nameOfType,
        ));
    }
}
