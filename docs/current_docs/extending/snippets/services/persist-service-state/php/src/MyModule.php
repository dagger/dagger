<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction, Doc};
use Dagger\Container;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Create Redis service and client')]
    public function redisSrv(): Container
    {
        $redisSrv = dag()
            ->container()
            ->from('redis')
            ->withExposedPort(6379)
            ->withMountedCache('/data', dag()->cacheVolume('my-redis'))
            ->withWorkdir('/data')
            ->asService(useEntrypoint: true);

            $redisCLI = dag()
                ->container()
                ->from('redis')
                ->withServiceBinding('redis-srv', $redisSrv)
                ->withEntrypoint(['redis-cli', '-h', 'redis-srv']);

            return $redisCLI;
    }

    #[DaggerFunction]
    #[Doc('Set key and value in Redis service')]
    public function set(
        #[Doc('The cache key to set')]
        string $key,
        #[Doc('The cache value to set')]
        string $value
    ): string {
        return this->redis()
            ->withExec(['set', $key, $value], useEntrypoint: true)
            ->withExec(['save'], useEntrypoint: true)
            ->stdout();
    }

    #[DaggerFunction]
    #[Doc('Get value from Redis service')]
    public function get(
        #[Doc('The cache key to get')]
        string $key,
    ): string {
        return this->redis()
            ->withExec(['get', $key], useEntrypoint: true)
            ->stdout();
    }
}
