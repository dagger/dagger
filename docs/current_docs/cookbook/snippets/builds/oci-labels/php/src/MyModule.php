<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Build and publish image with OCI labelss')]
    public function build(): string
    {
        $ref = dag()
          ->container()
          ->from('alpine')
          ->withLabel('org.opencontainers.image.title', 'my-alpine')
          ->withLabel('org.opencontainers.image.version', '1.0')
          ->withLabel('org.opencontainers.image.created', date('c'))
          ->withLabel('org.opencontainers.image.source', 'https://github.com/alpinelinux/docker-alpine')
          ->withLabel('org.opencontainers.image.licenses', 'MIT')
          ->publish('ttl.sh/hello-dagger');

        return $ref;
    }
}
