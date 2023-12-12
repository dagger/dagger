<?php

namespace Dagger\Client;

use BackedEnum;
use GraphQL\QueryBuilder\QueryBuilder;

class DaggerQueryBuilder extends QueryBuilder
{
    public function setArgument(string $argumentName, $argumentValue): QueryBuilder|\GraphQL\QueryBuilder\AbstractQueryBuilder
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
