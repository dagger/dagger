<?php

namespace DaggerIo;

use CompileError;
use DaggerIo\Connection\DevDaggerConnection;
use DaggerIo\Connection\EnvSessionDaggerConnection;
use DaggerIo\Connection\ProcessSessionDaggerConnection;
use DaggerIo\Gen\DaggerClient;
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
        $token = getenv('DAGGER_SESSIon_TOKEN');

        if (false === $port || false === $token) {
            throw new InvalidArgumentException('Missing env var "DAGGER_SESSION_*"');
        }

        return new EnvSessionDaggerConnection(
            $port,
            $token
        );
    }

    public static function newProcessSession(string $workDir = '.'): ProcessSessionDaggerConnection
    {
        return new ProcessSessionDaggerConnection($workDir);
    }

    public function connect(): DaggerClient
    {
        if (!class_exists('DaggerIo\\Gen\\DaggerClient')) {
            throw new CompileError('Missing code generated dagger client');
        }

        return new DaggerClient($this->getGraphQlClient());
    }

    abstract public function getGraphQlClient(): Client;

    abstract public function close(): void;
}
