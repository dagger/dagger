<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\Directory;
use Dagger\File;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Build and publish image from Dockerfile')]
    public function build(
        #[Doc('location of source directory')]
        Directory $src,
        #[Doc('location of dockerfile')]
        File $dockerfile,
    ): string {
        $workspace = dag()
            ->container()
            ->withDirectory('/src', $src)
            ->withWorkdir('/src')
            ->withFile('/src/custom.Dockerfile', $dockerfile)
            ->directory('/src');

        // build using Dockerfile and publish to registry
        $ref = dag()
          ->container()
          ->build($workspace, 'custom.Dockerfile')
          ->publish('ttl.sh/hello-dagger');

        return $ref;
    }
}
