<?php

namespace Dagger\Tests\Integration\GraphQl;

use Dagger\GraphQl\QueryBuilderChain;
use GraphQL\QueryBuilder\QueryBuilder;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\TestCase;

#[Group('integration')]
class QueryBuilderChainTest extends TestCase
{
    public function testChain(): void
    {
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

        $queryFromId = $queryChainContainerFromId->getFullQuery()->__toString();
        $queryId = $queryChainContainerId->getFullQuery()->__toString();

        // language=graphql
        $expectedQueryFromId = <<<'GQL'
query {
container {
from(address: "alpine:latest") {
id
}
}
}
GQL;
        // language=graphql
        $expectedQueryId = <<<'GQL'
query {
container {
id
}
}
GQL;

        self::assertEquals(trim($expectedQueryFromId), trim($queryFromId));
        self::assertEquals(trim($expectedQueryId), trim($queryId));
    }
}
