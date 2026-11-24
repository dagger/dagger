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

    /** @var array<string, true> */
    private array $served = [];

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
     * Serve the module at an address, at the commit a Git address is pinned to,
     * in this client's session, once. A failed serve is not recorded, so the
     * next call tries again.
     */
    public function serve(string $address, ?string $pin = null): void
    {
        $key = json_encode([$address, $pin]);
        if (isset($this->served[$key])) {
            return;
        }
        $serveModule = new \Dagger\Client\QueryBuilder('serveModule');
        $serveModule->setArgument('address', $address);
        if ($pin !== null) {
            $serveModule->setArgument('refPin', $pin);
        }
        $this->queryLeaf($serveModule, 'serveModule');
        $this->served[$key] = true;
    }

    /**
     * Load an object by its ID using node(id:) with an inline fragment.
     *
     * @template T of object
     * @param class-string<T> $className Fully-qualified PHP class name (e.g. \Dagger\Container)
     * @param Id $id The object's ID
     * @param ?string $graphQLTypeName GraphQL type name when it differs from the PHP class name
     * @return T
     */
    public function loadObjectFromId(string $className, Id $id, ?string $graphQLTypeName = null): object
    {
        $shortName = (new ReflectionClass($className))->getShortName();

        // Reverse the PHP class name → GraphQL type name mapping
        $graphQLTypeName ??= match ($shortName) {
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
