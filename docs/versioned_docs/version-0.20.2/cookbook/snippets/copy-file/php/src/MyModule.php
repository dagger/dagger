<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\Container;
use Dagger\File;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Return a container with a specified file')]
    public function copyFile(
        #[Doc('source file')]
        File $f,
    ): Container {
        $name = $f->name();
        return dag()
            ->container()
            ->from('alpine:latest')
            ->withFile("/src/$name", $f);

    }
}
