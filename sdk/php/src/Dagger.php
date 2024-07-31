<?php

declare(strict_types=1);

namespace Dagger;

use CompileError;

class Dagger
{
    private static Client $client;

    public static function getClientInstance(): Client
    {
        if (!isset(self::$client)) {
            self::$client = self::connect();
        }

        return self::$client;
    }

    public static function connect(string $workingDir = ''): Client
    {
        if (!class_exists('Dagger\\Client')) {
            throw new CompileError('Missing code generated dagger client');
        }

        $connection = Connection::get($workingDir);

        return new Client($connection);
    }
}
