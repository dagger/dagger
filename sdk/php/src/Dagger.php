<?php

namespace Dagger;

use CompileError;
use Dagger\Dagger\DaggerClient;

class Dagger
{
    public static function connect(string $workingDir = ''): DaggerClient
    {
        if (!class_exists('Dagger\\Dagger\\DaggerClient')) {
            throw new CompileError('Missing code generated dagger client');
        }

        $connection = Connection::get($workingDir);

        return new DaggerClient($connection);
    }
}
