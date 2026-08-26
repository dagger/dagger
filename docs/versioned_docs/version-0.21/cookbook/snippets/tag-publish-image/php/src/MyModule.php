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
    #[Doc('Tag a container image multiple times and publish it to a private registry')]
    public function publish(
        #[Doc('registry address')]
        string $registry,
        #[Doc('registry username')]
        string $username,
        #[Doc('registry password')]
        Secret $password,
    ): string {
        $tags = ['latest', '1.0-alpine', '1.0', '1.0.0'];
        $address = [];

        $container = dag()
            ->container()
            ->from('nginx:1.23-alpine')
            ->withNewFile('/usr/share/nginx/html/index.html', 'Hello from Dagger!', 400)
            ->withRegistryAuth($registry, $username, $password);

        foreach ($tags as $tag) {
          $a = $container->publish("$registry/$username/my-nginx:$tag");
          $address[] = $a;
        }

        return implode(',', $address);
    }
}
