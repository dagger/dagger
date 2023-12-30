<?php

namespace Dagger\Client;

use BackedEnum;
use GraphQL\QueryBuilder\AbstractQueryBuilder;
use GraphQL\QueryBuilder\QueryBuilder as GqlQueryBuilder;

class QueryBuilder extends GqlQueryBuilder
{
    public function setArgument(string $argumentName, $argumentValue): QueryBuilder|AbstractQueryBuilder
    {
        if ($argumentValue instanceof BackedEnum) {
            $value = $argumentValue->value;
        } elseif ($argumentValue instanceof IdAble) {
            $value = $argumentValue->id();
        } else {
            $value = $argumentValue;
        }

        if ($value instanceof DaggerScalar) {
            $value = $value->getValue();
        }

        return parent::setArgument($argumentName, $value);
    }
}
