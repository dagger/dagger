<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Client;

use Dagger\Attribute\GraphQLType;
use Dagger\Client;
use Dagger\Client\AbstractClient;
use Dagger\Client\AbstractObject;
use Dagger\Connection;
use Dagger\Id;
use GraphQL\Client as GqlClient;
use GraphQL\QueryBuilder\QueryBuilder;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(AbstractClient::class)]
final class AbstractClientTest extends TestCase
{
    #[Test]
    public function itLoadsObjectsUnderTheirGraphQLTypeName(): void
    {
        $connection = $this->createStub(Connection::class);
        $connection->method('connect')->willReturn($this->createStub(GqlClient::class));
        $client = new Client($connection);

        $named = $client->loadObjectFromId(GraphQLTypedObjectFixture::class, new Id('x'));
        self::assertStringContainsString('... on JSONValue { id }', $named->idQuery());

        $unnamed = $client->loadObjectFromId(UntypedObjectFixture::class, new Id('x'));
        self::assertStringContainsString('... on UntypedObjectFixture { id }', $unnamed->idQuery());

        $explicit = $client->loadObjectFromId(GraphQLTypedObjectFixture::class, new Id('x'), 'Other');
        self::assertStringContainsString('... on Other { id }', $explicit->idQuery());
    }
}

#[GraphQLType('JSONValue')]
final class GraphQLTypedObjectFixture extends AbstractObject
{
    public function idQuery(): string
    {
        return (string)$this->queryBuilderChain->chain(new QueryBuilder('id'))->getFullQuery();
    }
}

final class UntypedObjectFixture extends AbstractObject
{
    public function idQuery(): string
    {
        return (string)$this->queryBuilderChain->chain(new QueryBuilder('id'))->getFullQuery();
    }
}
