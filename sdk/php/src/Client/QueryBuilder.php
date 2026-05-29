<?php

namespace Dagger\Client;

use BackedEnum;
use GraphQL\QueryBuilder\AbstractQueryBuilder;
use GraphQL\QueryBuilder\QueryBuilder as GqlQueryBuilder;

class QueryBuilder extends GqlQueryBuilder
{
    public function setArgument(string $argumentName, $argumentValue): QueryBuilder|AbstractQueryBuilder
    {
        if ($argumentValue instanceof IdAble) {
            $argumentValue = $argumentValue->id();
        }

        if ($argumentValue instanceof AbstractScalar) {
            $argumentValue = $argumentValue->getValue();
        }

        return parent::setArgument($argumentName, $argumentValue);
    }
}
