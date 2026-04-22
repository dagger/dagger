<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\Container;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Build an application using cached dependencies')]
    public function build(
      #[Doc('Source code location')]
      Directory $source,
    ): Container {
        return dag()
            ->container()
            ->from('node:21')
            ->withDirectory('/src', $source)
            ->withWorkdir('/src')
            ->withMountedCache('/root/.npm', dag()->cacheVolume('node-21'))
            ->withExec(['npm', 'install']);
    }
}
