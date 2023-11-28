<?php

namespace DaggerIo;

use CompileError;
use DaggerIo\Connection\CliDownloader;
use DaggerIo\Connection\ProcessSessionDaggerConnection;
use DaggerIo\Gen\DaggerClient;

class Dagger
{
    public const DEFAULT_CLI_VERSION = '0.9.3';

    public static function connect(?string $workingDir, ?bool $dev = false): DaggerClient
    {
        $connection = null;

        if (!class_exists('DaggerIo\\Gen\\DaggerClient')) {
            throw new CompileError('Missing code generated dagger client');
        }

        if (true === $dev) {
            $connection = DaggerConnection::devConnection();
        }

        $port = getenv('DAGGER_SESSION_PORT');
        $token = getenv('DAGGER_SESSION_TOKEN');

        if (false !== $port || false !== $token) {
            $connection = DaggerConnection::newEnvSession();
        }

        if (null === $connection) {
            $cliDownloader = new CliDownloader(self::DEFAULT_CLI_VERSION);
            $connection = new ProcessSessionDaggerConnection($workingDir, $cliDownloader);
        }

        return new DaggerClient($connection);
    }
}
