<?php

namespace Dagger\GraphQl;

use GraphQL\InlineFragment;
use GraphQL\Query;
use GraphQL\QueryBuilder\QueryBuilder;

readonly class QueryBuilderChain
{
    /**
     * @param array<QueryBuilder> $queryStack
     * @param ?array{index: int, typeName: string} $inlineFragment
     */
    public function __construct(
        public array $queryStack = [],
        private ?array $inlineFragment = null,
    ) {
    }

    public function chain(QueryBuilder $innerQueryBuilder): self
    {
        return new self(
            array_merge($this->queryStack, [$innerQueryBuilder]),
            $this->inlineFragment,
        );
    }

    /**
     * Chain a QueryBuilder and mark it as requiring an inline fragment.
     * All subsequent selections will be wrapped in `... on $typeName { ... }`.
     *
     * This produces queries like: `node(id: "...") { ... on Container { field { ... } } }`
     */
    public function chainWithInlineFragment(QueryBuilder $queryBuilder, string $typeName): self
    {
        $newStack = array_merge($this->queryStack, [$queryBuilder]);
        return new self($newStack, [
            'index' => count($newStack) - 1,
            'typeName' => $typeName,
        ]);
    }

    public function getFullQuery(): Query
    {
        if ($this->inlineFragment !== null) {
            return $this->buildWithInlineFragment();
        }

        $rootQb = new QueryBuilder();
        $parentQb = $rootQb;

        foreach ($this->queryStack as $queryBuilder) {
            $qb = clone $queryBuilder;
            $parentQb->selectField($qb);
            $parentQb = $qb;
        }

        return $rootQb->getQuery();
    }

    private function buildWithInlineFragment(): Query
    {
        $fragmentIndex = $this->inlineFragment['index'];
        $typeName = $this->inlineFragment['typeName'];

        $rootQb = new QueryBuilder();
        $parentQb = $rootQb;

        // Build chain up to and including the fragment-bearing QB
        for ($i = 0; $i <= $fragmentIndex; $i++) {
            $qb = clone $this->queryStack[$i];
            $parentQb->selectField($qb);
            $parentQb = $qb;
        }

        // Build the remaining chain (after the fragment step) as nested QBs
        $remaining = array_slice($this->queryStack, $fragmentIndex + 1);

        $fragment = new InlineFragment($typeName);

        if (!empty($remaining)) {
            // Build nested chain from remaining QBs
            $first = clone $remaining[0];
            $current = $first;
            for ($i = 1; $i < count($remaining); $i++) {
                $next = clone $remaining[$i];
                $current->selectField($next);
                $current = $next;
            }

            $fragment->setSelectionSet([$first->getQuery()]);
        }

        // Attach the inline fragment to the node QB
        $parentQb->selectField($fragment);

        return $rootQb->getQuery();
    }
}
