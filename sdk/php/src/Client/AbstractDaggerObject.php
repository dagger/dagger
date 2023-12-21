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

    protected function queryLeaf(QueryBuilder $leafQueryBuilder, string $leafKey): array|string|null
    {
        $queryBuilderChain = $this->queryBuilderChain->chain($leafQueryBuilder);

        return $this->client->queryLeaf($queryBuilderChain->getFullQuery(), $leafKey);
    }

    protected function queryLeafDaggerScalar(QueryBuilder $leafQueryBuilder, string $leafKey, string $scalarObjectClass): DaggerScalar
    {
        $value = $this->queryLeaf($leafQueryBuilder, $leafKey);

        return new $scalarObjectClass($value);
    }
}
