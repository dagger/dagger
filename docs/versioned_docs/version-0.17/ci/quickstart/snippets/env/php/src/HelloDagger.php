<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\DefaultPath;
use Dagger\Attribute\Doc;
use Dagger\Container;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
class HelloDagger
{
    #[DaggerFunction]
    #[Doc('Build a ready-to-use development environment')]
    public function buildEnv(
        #[DefaultPath('/')]
        Directory $source,
    ): Container {
        $nodeCache = dag()
            ->cacheVolume('node');

        return dag()
            ->container()
            // start from a base Node.js container
            ->from('node:21-slim')
            // add the source code at /src
            ->withDirectory('/src', $source)
            // mount the cache volume at /root/.npm
            ->withMountedCache('/root/.npm', $nodeCache)
            // change the working directory to /src
            ->withWorkdir('/src')
            // run npm install to install dependencies
            ->withExec(['npm', 'install']);
    }
}
