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
    #[Doc('Publish the application container after building and testing it on-the-fly')]
    public function publish(
        #[DefaultPath('/')]
        Directory $source,
    ): string
    {
        $this->test($source);

        return $this
            ->build($source)
            ->publish('ttl.sh/hello-dagger-' . rand(0, 10000000));
    }

    #[DaggerFunction]
    #[Doc('Build the application container')]
    public function build(
        #[DefaultPath('/')]
        Directory $source,
    ): Container {
        $build = dag()
            ->node(null, $this->buildEnv($source))
            ->commands()
            ->run(['build'])
            ->directory('./dist');

        return dag()
          ->container()
          ->from('nginx:1.25-alpine')
          ->withDirectory('/usr/share/nginx/html', $build)
          ->withExposedPort(80);
    }

    #[DaggerFunction]
    #[Doc('Return the result of running unit tests')]
    public function test(
        #[DefaultPath('/')]
        Directory $source,
    ): string
    {
        return dag()
            ->node(null, $this->buildEnv($source))
            ->commands()
            ->run(['test:unit', 'run'])
            ->stdout();
    }

    #[DaggerFunction]
    #[Doc('Build a ready-to-use development environment')]
    public function buildEnv(
        #[DefaultPath('/')]
        Directory $source,
    ): Container
    {
        return dag()
            ->node('21')
            ->withNpm()
            ->withSource($source)
            ->install()
            ->container();
    }
}
