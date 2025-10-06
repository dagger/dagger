<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    // Build and publish image from existing Dockerfile
    #[DaggerFunction]
    public function build(
        // location of directory containing Dockerfile
        Directory $src,
    ): string {
        $ref = $src
        ->dockerBuild() // build from Dockerfile
        ->publish('ttl.sh/hello-dagger');
        return $ref;
    }
}
