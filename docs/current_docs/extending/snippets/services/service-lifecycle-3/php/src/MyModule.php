<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction, Doc};

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Creates Redis service and client')]
    public function redisService(): string
    {
        $redisSrv = dag
            ->container()
            ->from('redis')
            ->withExposedPort(6379)
            ->asService(useEntrypoint: true);

        // create Redis client container
        $redisCLI = dag
            ->container()
            ->from('redis')
            ->withServiceBinding('redis-srv', redisSrv);

        $args = ['redis-cli', '-h', 'redis-srv'];

        // set and get value
        return redisCLI
            ->withExec([...args, 'set', 'foo', 'abc'])
            ->withExec([...args, 'get', 'foo'])
            ->stdout();
    }
}
