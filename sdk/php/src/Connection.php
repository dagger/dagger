<?php

namespace Dagger;

use Dagger\Connection\CliDownloader;
use Dagger\Connection\EnvSessionConnection;
use Dagger\Connection\ProcessSessionConnection;
use GraphQL\Client;
use InvalidArgumentException;

abstract class Connection
{
    protected ?Client $client;

    public static function get(string $workingDir = ''): Connection
    {
        $connection = static::newEnvSession();

        if (!empty($workingDir)) {
            throw new InvalidArgumentException(
                'cannot configure workdir for existing session' .
                ' (please use --workdir or host.directory with absolute paths instead)'
            );
        }

        if (null === $connection) {
            $connection = static::newProcessSession($workingDir, new CliDownloader());
        }

        return $connection;
    }

    public static function newEnvSession(): ?EnvSessionConnection
    {
        $port = getenv('DAGGER_SESSION_PORT');
        $token = getenv('DAGGER_SESSION_TOKEN');

        if (false === $port || false === $token) {
            return null;
        }

        return new EnvSessionConnection();
    }

    /**
     * @deprecated
     * dagger modules will always have the environment variables set
     * so we don't need to download a CLI Client
     */
    public static function newProcessSession(string $workDir, CliDownloader $cliDownloader): ProcessSessionConnection
    {
        return new ProcessSessionConnection($workDir, $cliDownloader);
    }

    protected static function createGraphQlClient(int $port, string $token): Client
    {
        $encodedToken = base64_encode("{$token}:");

        return new Client("http://127.0.0.1:{$port}/query", [
            'Authorization' => "Basic {$encodedToken}",
        ]);
    }

    abstract public function connect(): Client;

    abstract public function close(): void;
}
