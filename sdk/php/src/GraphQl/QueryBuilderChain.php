<?php

namespace Dagger\GraphQl;

use GraphQL\Query;
use GraphQL\QueryBuilder\QueryBuilder;

readonly class QueryBuilderChain
{
    /**
     * @param array<QueryBuilder> $queryStack
     */
    public function __construct(
        public array $queryStack = []
    ) {
    }

    public function chain(QueryBuilder $innerQueryBuilder): self
    {
        return new self(array_merge($this->queryStack, [$innerQueryBuilder]));
    }

    public function getFullQuery(): Query
    {
        $rootQb = new QueryBuilder();
        $parentQb = $rootQb;

        foreach ($this->queryStack as $queryBuilder) {
            $qb = clone $queryBuilder;
            $parentQb->selectField($qb);
            $parentQb = $qb;
        }

        return $rootQb->getQuery();
    }
}
