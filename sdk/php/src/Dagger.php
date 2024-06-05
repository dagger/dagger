<?php
declare(strict_types=1);

namespace Dagger;

use CompileError;

class Dagger
{
    public static function connect(string $workingDir = ''): Client
    {
        if (!class_exists('Dagger\\Client')) {
            throw new CompileError('Missing code generated dagger client');
        }

        $connection = Connection::get($workingDir);

        return new Client($connection);
    }
}
