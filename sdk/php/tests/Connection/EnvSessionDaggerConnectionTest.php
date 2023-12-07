<?php

namespace DaggerIo\Tests\Connection;

use DaggerIo\DaggerConnection;
use PHPUnit\Framework\TestCase;

class EnvSessionDaggerConnectionTest extends TestCase
{
    public function testConnection(): void
    {
        $conn = DaggerConnection::newEnvSession();
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
