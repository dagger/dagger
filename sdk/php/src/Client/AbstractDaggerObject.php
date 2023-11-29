<?php

namespace DaggerIo\Client;

use DaggerIo\GraphQl\QueryBuilderChain;
use GraphQL\QueryBuilder\QueryBuilder;

abstract class AbstractDaggerObject
{
    public function __construct(
        protected readonly AbstractDaggerClient $client,
        protected readonly QueryBuilderChain $queryBuilderChain
    ) {
    }

    protected function queryLeaf(QueryBuilder $leafQueryBuilder, string $leafKey): null|array|string|int|float|bool
    {
        $queryBuilderChain = $this->queryBuilderChain->chain($leafQueryBuilder);

        return $this->client->queryLeaf($queryBuilderChain->getFullQuery(), $leafKey);
    }
}
