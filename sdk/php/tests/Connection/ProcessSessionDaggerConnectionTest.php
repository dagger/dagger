<?php

namespace DaggerIo\Tests\Connection;

use DaggerIo\Connection\ProcessSessionDaggerConnection;
use DaggerIo\Dagger;
use DaggerIo\DaggerConnection;
use PHPUnit\Framework\TestCase;

class ProcessSessionDaggerConnectionTest extends TestCase
{
    public static function getConnection(): ProcessSessionDaggerConnection
    {
        $testWorkDir = implode(DIRECTORY_SEPARATOR, [__DIR__, '..', 'Resources', 'workDir']);

        // Use the dagger binary provided by our dev env
        putenv('_EXPERIMENTAL_DAGGER_CLI_BIN=dagger');

        return DaggerConnection::newProcessSession($testWorkDir, Dagger::DEFAULT_CLI_VERSION);
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
