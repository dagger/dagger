<?php

namespace Dagger\Client;

use Dagger\Client;
use Dagger\Connection;
use Dagger\GraphQl\QueryBuilderChain;
use Dagger\Id;
use GraphQL\Client as GqlClient;
use GraphQL\Query;
use GraphQL\QueryBuilder\QueryBuilder;
use GraphQL\Results;
use RecursiveArrayIterator;
use RecursiveIteratorIterator;
use ReflectionClass;

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

    /**
     * Load an object by its ID using node(id:) with an inline fragment.
     *
     * @template T of object
     * @param class-string<T> $className Fully-qualified PHP class name (e.g. \Dagger\Container)
     * @param Id $id The object's ID
     * @return T
     */
    public function loadObjectFromId(string $className, Id $id): object
    {
        $shortName = (new ReflectionClass($className))->getShortName();

        // Reverse the PHP class name → GraphQL type name mapping
        $graphQLTypeName = match ($shortName) {
            'Function_' => 'Function',
            'Client' => 'Query',
            default => $shortName,
        };

        $nodeQb = new \Dagger\Client\QueryBuilder('node');
        $nodeQb->setArgument('id', $id);
        $chain = $this->queryBuilderChain->chainWithInlineFragment($nodeQb, $graphQLTypeName);

        return new $className($this, $chain);
    }
}
