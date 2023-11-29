<?php

/**
 * This class has been generated by dagger-php-sdk. DO NOT EDIT.
 */

declare(strict_types=1);

namespace DaggerIo\Gen;

/**
 * Function represents a resolver provided by a Module.
 *
 * A function always evaluates against a parent object and is given a set of
 * named arguments.
 */
class DaggerFunction extends \DaggerIo\Client\AbstractDaggerObject implements \DaggerIo\Client\IdAble
{
    /**
     * Arguments accepted by this function, if any
     */
    public function args(): array
    {
        $leafQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('args');
        return (array)$this->queryLeaf($leafQueryBuilder, 'args');
    }

    /**
     * A doc string for the function, if any
     */
    public function description(): string
    {
        $leafQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('description');
        return (string)$this->queryLeaf($leafQueryBuilder, 'description');
    }

    /**
     * The ID of the function
     */
    public function id(): FunctionId
    {
        $leafQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('id');
        return new \DaggerIo\Gen\FunctionId((string)$this->queryLeaf($leafQueryBuilder, 'id'));
    }

    /**
     * The name of the function
     */
    public function name(): string
    {
        $leafQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('name');
        return (string)$this->queryLeaf($leafQueryBuilder, 'name');
    }

    /**
     * The type returned by this function
     */
    public function returnType(): TypeDef
    {
        $innerQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('returnType');
        return new \DaggerIo\Gen\TypeDef($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Returns the function with the provided argument
     */
    public function withArg(
        string $name,
        TypeDefId|TypeDef $typeDef,
        ?string $description = null,
        ?Json $defaultValue = null,
    ): DaggerFunction
    {
        $innerQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('withArg');
        $innerQueryBuilder->setArgument('name', $name);
        $innerQueryBuilder->setArgument('typeDef', $typeDef);
        if (null !== $description) {
        $innerQueryBuilder->setArgument('description', $description);
        }
        if (null !== $defaultValue) {
        $innerQueryBuilder->setArgument('defaultValue', $defaultValue);
        }
        return new \DaggerIo\Gen\DaggerFunction($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }

    /**
     * Returns the function with the doc string
     */
    public function withDescription(string $description): DaggerFunction
    {
        $innerQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('withDescription');
        $innerQueryBuilder->setArgument('description', $description);
        return new \DaggerIo\Gen\DaggerFunction($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }
}
