<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Container;
use Dagger\File;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function readFileHttp(
        string $url,
    ): Container {
      	$file = dag()->http($url);
        return dag()
            ->container()
            ->from('alpine:latest')
            ->withFile('/src/myfile', $file);
    }
}
