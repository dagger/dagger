<?php

namespace DaggerIo\Tests\GraphQl;

use DaggerIo\DaggerConnection;
use DaggerIo\GraphQl\QueryBuilderChain;
use GraphQL\QueryBuilder\QueryBuilder;
use PHPUnit\Framework\TestCase;

class QueryBuilderChainTest extends TestCase
{
    public function testChain(): void
    {
        $connection = DaggerConnection::newEnvSession();
        $graphQlClient = $connection->getGraphQlClient();

        $queryChain = new QueryBuilderChain();
        $queryChainContainer = $queryChain->chain(new QueryBuilder('container'));
        $queryChainContainerId = $queryChainContainer->chain(new QueryBuilder('id'));

        $queryChainContainerFrom = $queryChainContainer->chain(
            (new QueryBuilder('from'))
                ->setArgument('address', 'alpine:latest')
        );

        $queryChainContainerFromId = $queryChainContainerFrom->chain(
            new QueryBuilder('id')
        );

        $queryFromId = $queryChainContainerFromId->getFullQuery();
        $resultFromId = $graphQlClient->runQuery($queryFromId)->getData();

        $queryId = $queryChainContainerId->getFullQuery();
        $resultId = $graphQlClient->runQuery($queryId)->getData();

        $this->assertObjectNotHasProperty('from', $resultId->container);
        $this->assertObjectNotHasProperty('id', $resultFromId->container);
        $this->assertStringStartsWith('core.Container', $resultFromId->container->from->id);
        $this->assertStringStartsWith('core.Container', $resultId->container->id);
    }
}
