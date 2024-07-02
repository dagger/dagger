<?php

namespace Dagger\Tests\Integration\Connection;

use Dagger\Connection;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\TestCase;

#[Group('integration')]
class ConnectionTest extends TestCase
{
    private static array $daggerEnvVars = [];

    private static array $daggerEnvVarNames = [
        'DAGGER_SESSION_PORT',
        'DAGGER_SESSION_TOKEN',
        '_EXPERIMENTAL_DAGGER_CLI_BIN',
    ];

    public static function setUpBeforeClass(): void
    {
        foreach (self::$daggerEnvVarNames as $varName) {
            $previousValue = getenv($varName);
            if (false !== $previousValue) {
                self::$daggerEnvVars[$varName] = $previousValue;
            }
        }
    }

    public static function tearDownAfterClass(): void
    {
        // restore env vars
        foreach (self::$daggerEnvVars as $varName => $varValue) {
            putenv("{$varName}={$varValue}");
        }
    }

    /**
     * Reset env vars for every tests.
     */
    public function setUp(): void
    {
        foreach (self::$daggerEnvVarNames as $varName) {
            putenv($varName); // unset env var
        }
    }

    public function testReturnEnvConnectionWithEnvVars(): void
    {
        putenv('DAGGER_SESSION_PORT=52037');
        putenv('DAGGER_SESSION_TOKEN=189de95f-07df-415d-b42a-7851c731359d');

        $connection = Connection::newEnvSession();
        $this->assertInstanceOf(Connection\EnvSessionConnection::class, $connection);
    }

    public function testReturnEmptyConnectionWhenEnvNotSet(): void
    {
        $connection = Connection::newEnvSession();
        $this->assertNull($connection);

        putenv('DAGGER_SESSION_PORT=52037');
        putenv('DAGGER_SESSION_TOKEN');

        $connection = Connection::newEnvSession();
        $this->assertNull($connection);

        putenv('DAGGER_SESSION_PORT');
        putenv('DAGGER_SESSION_TOKEN=189de95f-07df-415d-b42a-7851c731359d');

        $connection = Connection::newEnvSession();
        $this->assertNull($connection);
    }

    public function testReturnConnectionFromDynamicProvisioning(): void
    {
        putenv('DAGGER_SESSION_PORT');
        putenv('DAGGER_SESSION_TOKEN');

        $connection = Connection::get('');

        $this->assertInstanceOf(Connection\ProcessSessionConnection::class, $connection);
    }

    public function testReturnConnectionFromEnvWithEnvVars(): void
    {
        putenv('DAGGER_SESSION_PORT=52037');
        putenv('DAGGER_SESSION_TOKEN=189de95f-07df-415d-b42a-7851c731359d');

        $connection = Connection::get();

        $this->assertInstanceOf(Connection\EnvSessionConnection::class, $connection);
    }

    public function testCliDownloadIsCalledWhenCliBinIsNull(): void
    {
        putenv('_EXPERIMENTAL_DAGGER_CLI_BIN');
        $cliBinPath = self::$daggerEnvVars['_EXPERIMENTAL_DAGGER_CLI_BIN'];

        $cliDownloader = $this->createMock(Connection\CliDownloader::class);
        $cliDownloader
            ->expects($this->once())
            ->method('download')
            ->willReturn($cliBinPath);
        $processSession = Connection::newProcessSession('', $cliDownloader);
        $processSession->connect();
    }
}
