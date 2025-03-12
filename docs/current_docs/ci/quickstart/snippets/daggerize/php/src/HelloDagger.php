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
class HelloDagger
{
    #[DaggerFunction]
    #[Doc('Publish the application container after building and testing it on-the-fly')]
    public function publish(Directory $source): string
    {
        $this->test($source);

        return $this
            ->build($source)
            ->publish('ttl.sh/hello-dagger-' . rand(0, 10000000));
    }

    #[DaggerFunction]
    #[Doc('Build the application container')]
    public function build(Directory $source): Container
    {
        $build = $this
            ->buildEnv($source)
            ->withExec(['npm', 'run', 'build'])
            ->directory('./dist');

        return dag()
            ->container()
            ->from('nginx:1.25-alpine')
            ->withDirectory('/usr/share/nginx/html', $build)
            ->withExposedPort(80);
    }

    #[DaggerFunction]
    #[Doc('Return the result of running unit tests')]
    public function test(Directory $source): string
    {
        return $this
            ->buildEnv($source)
            ->withExec(['npm', 'run', 'test:unit', 'run'])
            ->stdout();
    }

    #[DaggerFunction]
    #[Doc('Build a ready-to-use development environment')]
    public function buildEnv(Directory $source): Container
    {
        $nodeCache = dag()
            ->cacheVolume('node');

        return dag()
            ->container()
            ->from('node:21-slim')
            ->withDirectory('/src', $source)
            ->withMountedCache('/root/.npm', $nodeCache)
            ->withWorkdir('/src')
            ->withExec(['npm', 'install']);
    }
}
