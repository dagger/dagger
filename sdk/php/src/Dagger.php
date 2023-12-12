<?php

namespace DaggerIo;

use CompileError;
use DaggerIo\Gen\DaggerClient;

class Dagger
{
    public static function connect(string $workingDir = ''): DaggerClient
    {
        if (!class_exists('DaggerIo\\Gen\\DaggerClient')) {
            throw new CompileError('Missing code generated dagger client');
        }

        $connection = Connection::get($workingDir);

        return new DaggerClient($connection);
    }
}
