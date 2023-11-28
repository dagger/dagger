<?php

namespace DaggerIo\Tests\Connection;

use DaggerIo\Connection\ProcessSessionDaggerConnection;
use DaggerIo\DaggerConnection;
use PHPUnit\Framework\TestCase;

class ProcessSessionDaggerConnectionTest extends TestCase
{
    public static function getConnection(): ProcessSessionDaggerConnection
    {
        return DaggerConnection::newProcessSession(
            __DIR__.DIRECTORY_SEPARATOR.'..'.DIRECTORY_SEPARATOR.'..'.DIRECTORY_SEPARATOR.'.dagger'
        );
    }

    public function testVersion(): void
    {
        $connection = self::getConnection();
        $version = $connection->getVersion();
        $this->assertStringStartsWith('dagger', $version);
    }

    public function testConnectionConstructor(): void
    {
        $connection = self::getConnection();
        $client = $connection->getGraphQlClient();
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
