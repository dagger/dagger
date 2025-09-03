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
    // Return a container with a specified file
    public function copyFile(
        // source file
        File $f,
    ): Container {
        $name = $f->name();
        return dag()
            ->container()
            ->from('alpine:latest')
            ->withFile("/src/$name", $f);

    }
}
