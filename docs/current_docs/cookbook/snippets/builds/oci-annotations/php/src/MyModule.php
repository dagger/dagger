<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Build and publish image with OCI annotations')]
    public function build(): string
    {
        $address = dag()
            ->container()
            ->from('alpine:latest')
            ->withExec(['apk', 'add', 'git'])
            ->withWorkdir('/src')
            ->withExec(['git', 'clone', 'https://github.com/dagger/dagger', '.'])
            ->withAnnotation(
                'org.opencontainers.image.authors',
                'John Doe'
            )
            ->withAnnotation(
                'org.opencontainers.image.title',
                'Dagger source',
            )
            ->publish('ttl.sh/custom-image-' . rand(0, 9999999));

        return $address;
    }
}
