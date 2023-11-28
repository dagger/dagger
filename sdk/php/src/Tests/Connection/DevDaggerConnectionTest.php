<?php

namespace DaggerIo\Tests\Connection;

use DaggerIo\DaggerConnection;
use PHPUnit\Framework\TestCase;

class DevDaggerConnectionTest extends TestCase
{
    public function testConnection(): void
    {
        $conn = DaggerConnection::devConnection();
        $client = $conn->getGraphQlClient();
        // language=graphql
        $query = <<<'QUERY'
            query {
                container {
                    id
                }
            }
        QUERY;

        $result = $client->runRawQuery($query)->getData();
        $this->assertStringStartsWith('core.Container', $result->container->id);
    }
}
