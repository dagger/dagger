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
    #[Doc('Build the application container')]
    public function build(
      #[DefaultPath('/')]
      Directory $source,
    ): Container {
        $build = $this
            // get the build environment container
            // by calling another Dagger Function
            ->buildEnv($source)
            // build the application
            ->withExec(['npm', 'run', 'build'])
            // get the build output directory
            ->directory('./dist');

        return dag()
            ->container()
            // start from a slim NGINX container
            ->from('nginx:1.25-alpine')
            // copy the build output directory to the container
            ->withDirectory('/usr/share/nginx/html', $build)
            // expose the container port
            ->withExposedPort(80);
    }
}
