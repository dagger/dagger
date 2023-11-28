<?php

namespace DaggerIo\Client;

use DaggerIo\GraphQl\QueryBuilderChain;
use GraphQL\Client;
use GraphQL\Query;
use GraphQL\QueryBuilder\QueryBuilder;
use GraphQL\Results;
use RecursiveArrayIterator;
use RecursiveIteratorIterator;

abstract class AbstractDaggerClient
{
    protected QueryBuilderChain $queryBuilderChain;
    protected AbstractDaggerClient $client;

    public function __construct(protected readonly Client $graphQlClient)
    {
        $this->queryBuilderChain = new QueryBuilderChain();
        $this->client = $this;
    }

    public function runQuery(QueryBuilder|Query $query): Results
    {
        return $this->graphQlClient->runQuery($query);
    }

    public function queryLeaf(QueryBuilder|Query $query, string $leafKey): null|string|array
    {
        $response = $this->graphQlClient->runQuery($query);
        $data = $response->getData();
        foreach (new RecursiveIteratorIterator(
            new RecursiveArrayIterator($data), RecursiveIteratorIterator::CHILD_FIRST) as $k => $value) {
            if ($k === $leafKey) {
                return $value;
            }
        }

        return null;
    }
}
