<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\Container;
use Dagger\File;
use Dagger\Secret;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Publish a container image to a private registry')]
    public function publish(
        #[Doc('registry address')]
        string $registry,
        #[Doc('registry username')]
        string $username,
        #[Doc('registry password')]
        Secret $password,
    ): string {
        return dag()
            ->container()
            ->from('nginx:1.23-alpine')
            ->withNewFile('/usr/share/nginx/html/index.html', 'Hello from Dagger!', 400)
            ->withRegistryAuth($registry, $username, $password)
            ->publish("$registry/$username/my-nginx");
    }
}
