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
            ->withServiceBinding('redis-srv', redisSrv)
            ->withEntrypoint();

        $args = ['redis-cli', '-h', 'redis-srv'];

        // set value
        $setter = redisCLI
            ->withExec([...args, 'set', 'foo', 'abc'])
            ->stdout();

        // get value
        $getter = redisCLI->withExec([...args, 'get', 'foo'])->stdout();

        return setter . getter;
    }
}
