<?php

namespace DaggerIo;

use DaggerIo\Connection\CliDownloader;
use DaggerIo\Connection\DevDaggerConnection;
use DaggerIo\Connection\EnvSessionDaggerConnection;
use DaggerIo\Connection\ProcessSessionDaggerConnection;
use GraphQL\Client;
use InvalidArgumentException;

abstract class DaggerConnection
{
    protected static ?DevDaggerConnection $devConnectionInstance = null;

    public static function devConnection(): DevDaggerConnection
    {
        if (null === static::$devConnectionInstance) {
            static::$devConnectionInstance = new DevDaggerConnection();
        }

        return static::$devConnectionInstance;
    }

    public static function newEnvSession(): EnvSessionDaggerConnection
    {
        $port = getenv('DAGGER_SESSION_PORT');
        $token = getenv('DAGGER_SESSION_TOKEN');

        if (false === $port || false === $token) {
            throw new InvalidArgumentException('Missing env var "DAGGER_SESSION_*"');
        }

        return new EnvSessionDaggerConnection(
            $port,
            $token
        );
    }

    public static function newProcessSession(string $workDir, string $version): ProcessSessionDaggerConnection
    {
        return new ProcessSessionDaggerConnection($workDir, new CliDownloader($version));
    }

    abstract public function getGraphQlClient(): Client;

    abstract public function close(): void;
}
