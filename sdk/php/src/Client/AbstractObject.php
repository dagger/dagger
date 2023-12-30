<?php

namespace Dagger\Client;

use Dagger\GraphQl\QueryBuilderChain;
use GraphQL\QueryBuilder\QueryBuilder;

abstract class AbstractObject
{
    public function __construct(
        protected readonly AbstractClient $client,
        protected readonly QueryBuilderChain $queryBuilderChain
    ) {
    }

    protected function queryLeaf(QueryBuilder $leafQueryBuilder, string $leafKey): null|array|string|int|float|bool
    {
        $queryBuilderChain = $this->queryBuilderChain->chain($leafQueryBuilder);

        return $this->client->queryLeaf($queryBuilderChain->getFullQuery(), $leafKey);
    }
}
