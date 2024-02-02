<?php

/**
 * This class has been generated by dagger-php-sdk. DO NOT EDIT.
 */

declare(strict_types=1);

namespace Dagger;

/**
 * A definition of a parameter or return type in a Module.
 */
class TypeDef extends Client\AbstractObject implements Client\IdAble
{
    /**
     * If kind is INPUT, the input-specific type definition. If kind is not INPUT, this will be null.
     */
    public function asInput(): InputTypeDef
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('asInput');
        return new \Dagger\InputTypeDef($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * If kind is INTERFACE, the interface-specific type definition. If kind is not INTERFACE, this will be null.
     */
    public function asInterface(): InterfaceTypeDef
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('asInterface');
        return new \Dagger\InterfaceTypeDef($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * If kind is LIST, the list-specific type definition. If kind is not LIST, this will be null.
     */
    public function asList(): ListTypeDef
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('asList');
        return new \Dagger\ListTypeDef($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * If kind is OBJECT, the object-specific type definition. If kind is not OBJECT, this will be null.
     */
    public function asObject(): ObjectTypeDef
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('asObject');
        return new \Dagger\ObjectTypeDef($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * A unique identifier for this TypeDef.
     */
    public function id(): TypeDefId
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('id');
        return new \Dagger\TypeDefId((string)$this->queryLeaf($leafQueryBuilder, 'id'));
    }

    /**
     * The kind of type this is (e.g. primitive, list, object).
     */
    public function kind(): TypeDefKind
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('kind');
        return \Dagger\TypeDefKind::from((string)$this->queryLeaf($leafQueryBuilder, 'kind'));
    }

    /**
     * Whether this type can be set to null. Defaults to false.
     */
    public function optional(): bool
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('optional');
        return (bool)$this->queryLeaf($leafQueryBuilder, 'optional');
    }

    /**
     * Adds a function for constructing a new instance of an Object TypeDef, failing if the type is not an object.
     */
    public function withConstructor(FunctionId|Function_ $function): TypeDef
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withConstructor');
        $innerQueryBuilder->setArgument('function', $function);
        return new \Dagger\TypeDef($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Adds a static field for an Object TypeDef, failing if the type is not an object.
     */
    public function withField(string $name, TypeDefId|TypeDef $typeDef, ?string $description = ''): TypeDef
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withField');
        $innerQueryBuilder->setArgument('name', $name);
        $innerQueryBuilder->setArgument('typeDef', $typeDef);
        if (null !== $description) {
        $innerQueryBuilder->setArgument('description', $description);
        }
        return new \Dagger\TypeDef($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Adds a function for an Object or Interface TypeDef, failing if the type is not one of those kinds.
     */
    public function withFunction(FunctionId|Function_ $function): TypeDef
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withFunction');
        $innerQueryBuilder->setArgument('function', $function);
        return new \Dagger\TypeDef($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Returns a TypeDef of kind Interface with the provided name.
     */
    public function withInterface(string $name, ?string $description = ''): TypeDef
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withInterface');
        $innerQueryBuilder->setArgument('name', $name);
        if (null !== $description) {
        $innerQueryBuilder->setArgument('description', $description);
        }
        return new \Dagger\TypeDef($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Sets the kind of the type.
     */
    public function withKind(TypeDefKind $kind): TypeDef
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withKind');
        $innerQueryBuilder->setArgument('kind', $kind);
        return new \Dagger\TypeDef($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Returns a TypeDef of kind List with the provided type for its elements.
     */
    public function withListOf(TypeDefId|TypeDef $elementType): TypeDef
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withListOf');
        $innerQueryBuilder->setArgument('elementType', $elementType);
        return new \Dagger\TypeDef($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Returns a TypeDef of kind Object with the provided name.
     *
     * Note that an object's fields and functions may be omitted if the intent is only to refer to an object. This is how functions are able to return their own object, or any other circular reference.
     */
    public function withObject(string $name, ?string $description = ''): TypeDef
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withObject');
        $innerQueryBuilder->setArgument('name', $name);
        if (null !== $description) {
        $innerQueryBuilder->setArgument('description', $description);
        }
        return new \Dagger\TypeDef($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Sets whether this type can be set to null.
     */
    public function withOptional(bool $optional): TypeDef
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('withOptional');
        $innerQueryBuilder->setArgument('optional', $optional);
        return new \Dagger\TypeDef($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }
}
