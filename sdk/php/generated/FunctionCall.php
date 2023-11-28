<?php

/**
 * This class has been generated by dagger-php-sdk. DO NOT EDIT.
 */

declare(strict_types=1);

namespace DaggerIo\Gen;

class FunctionCall extends \DaggerIo\Client\AbstractDaggerObject
{
    /**
     * The argument values the function is being invoked with.
     */
    public function inputArgs(): array
    {
        $leafQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('inputArgs');
        return $this->queryLeaf($leafQueryBuilder, 'inputArgs');
    }

    /**
     * The name of the function being called.
     */
    public function name(): string
    {
        $leafQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('name');
        return $this->queryLeaf($leafQueryBuilder, 'name');
    }

    /**
     * The value of the parent object of the function being called.
     * If the function is "top-level" to the module, this is always an empty object.
     */
    public function parent(): Json
    {
        $leafQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('parent');
        return $this->queryLeafDaggerScalar($leafQueryBuilder, 'parent', \DaggerIo\Gen\Json::class);
    }

    /**
     * The name of the parent object of the function being called.
     * If the function is "top-level" to the module, this is the name of the module.
     */
    public function parentName(): string
    {
        $leafQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('parentName');
        return $this->queryLeaf($leafQueryBuilder, 'parentName');
    }

    /**
     * Set the return value of the function call to the provided value.
     * The value should be a string of the JSON serialization of the return value.
     */
    public function returnValue(Json $value): NullVoid
    {
        $leafQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder('returnValue');
        $leafQueryBuilder->setArgument('value', $value);
        return $this->queryLeafDaggerScalar($leafQueryBuilder, 'returnValue', \DaggerIo\Gen\NullVoid::class);
    }
}
