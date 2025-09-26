<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\Container;
use Dagger\Directory;
use Dagger\Secret;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Build a Container from a Dockerfile')]
    public function build(
        #[Doc('source code to build')]
        Directory $source,
        #[Doc('secret to use in the Dockerfile')]
        Secret $secret,
    ): Container {
        // ensure the Dagger secret's name matches what the Dockerfile
        // expects as the id for the secret mount.
        $buildSecret = dag()
            ->setSecret('gh-secret', $secret->plaintext());

        return $source
            ->dockerBuild(secrets: [$buildSecret]);
    }
}
