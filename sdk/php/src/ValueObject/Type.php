<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Client\IdAble;
use Dagger\TypeDefKind;
use ReflectionClass;
use ReflectionNamedType;
use ReflectionType;
use RuntimeException;

//@TODO support union/intersection types
//@TODO proper exception if no type is specified
final readonly class Type
{
    public TypeDefKind $typeDefKind;


    /**
     * @throws RuntimeException
     * if type is unsupported
     */
    public function __construct(
        public string $name,
        public bool $nullable = false,
    ) {
        $this->typeDefKind = $this->getTypeDefKind($name);
    }

    /**
     * @throws RuntimeException
     * if type is unsupported
     */
    public static function fromReflection(ReflectionType $type): self
    {
        if (!($type instanceof ReflectionNamedType)) {
            throw new RuntimeException(
                'union/intersection types are currently unsupported'
            );
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

    /**
     * @throws \RuntimeException if it is not a class
     */
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



    private function getTypeDefKind(string $nameOfType): TypeDefKind
    {
        switch ($nameOfType) {
            case 'bool': return TypeDefKind::BOOLEAN_KIND;
            case 'int': return TypeDefKind::INTEGER_KIND;
            case 'string': return TypeDefKind::STRING_KIND;
            case 'array': return TypeDefKind::LIST_KIND;
            case 'null':
            case 'void': return TypeDefKind::VOID_KIND;
        }

        if (class_exists($nameOfType)) {
            return TypeDefKind::OBJECT_KIND;
        }

        if (interface_exists($nameOfType)) {
            return TypeDefKind::INTERFACE_KIND;
        }

        throw new RuntimeException(sprintf(
            'No matching "%s" for "%s"',
            TypeDefKind::class,
            $nameOfType,
        ));
    }
}
