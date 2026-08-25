<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction, Doc};

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Explicitly start and stop a Redis service')]
    public function redisService(): string
    {
        $redisSrv = dag()
            ->container()
            ->from('redis')
            ->withExposedPort(6379)
            ->asService();

        // Start Redis ahead of time so it stays up for the duration of the test
        $redisSrv->start();

        // stop the service when done
        $redisSrv->stop();

        $redisCLI = dag()
            ->container()
            ->from('redis')
            ->withServiceBinding('redis-srv', $redisSrv);

        $args = ['redis-cli', '-h', 'redis-srv'];

        $setter = $redisCLI
            ->withExec([...$args, 'set', 'foo', 'abc'])
            ->stdout();

        $getter = $redisCLI
            ->withExec([...$args, 'get', 'foo'])
            ->stdout();

        return $setter . $getter;
    }
}
