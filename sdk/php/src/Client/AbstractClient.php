<?php

namespace Dagger\Client;

use Dagger\Client;
use Dagger\Connection;
use Dagger\GraphQl\QueryBuilderChain;
use GraphQL\Client as GqlClient;
use GraphQL\Query;
use GraphQL\QueryBuilder\QueryBuilder;
use GraphQL\Results;
use RecursiveArrayIterator;
use RecursiveIteratorIterator;

abstract class AbstractClient
{
    protected AbstractClient $client;
    protected GqlClient $graphQlClient;

    public function __construct(
        Connection|Client $clientOrConnection,
        protected readonly QueryBuilderChain $queryBuilderChain = new QueryBuilderChain()
    ) {
        if ($clientOrConnection instanceof Connection) {
            $this->graphQlClient = $clientOrConnection->connect();
        } else {
            $this->graphQlClient = $clientOrConnection->graphQlClient;
        }

        $this->client = $this;
    }

    public function runQuery(QueryBuilder|Query $query): Results
    {
        return $this->graphQlClient->runQuery($query);
    }

    public function queryLeaf(QueryBuilder|Query $query, string $leafKey): null|array|string|int|float|bool
    {
        $response = $this->graphQlClient->runQuery($query);
        $data = $response->getData();
        foreach (
            new RecursiveIteratorIterator(
                new RecursiveArrayIterator($data),
                RecursiveIteratorIterator::CHILD_FIRST
            ) as $k => $value
        ) {
            if ($k === $leafKey) {
                return $value;
            }
        }

        return null;
    }
}
