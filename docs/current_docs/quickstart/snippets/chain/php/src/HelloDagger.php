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
    #[Doc('Returns a base container')]
    public function base(): Container
    {
        return dag()
            ->container()
            ->from('cgr.dev/chainguard/wolfi-base');
    }

    #[DaggerFunction]
    #[Doc('Builds on top of base container and returns a new container')]
    public function build(): Container
    {
        return $this
            ->base()
            ->withExec(['apk', 'add', 'bash', 'git']);
    }

    #[DaggerFunction]
    #[Doc('Builds and publishes a container')]
    public function buildAndPublish(): string
    {
        return $this
            ->build()
            ->publish('ttl.sh/bar');
    }
}
