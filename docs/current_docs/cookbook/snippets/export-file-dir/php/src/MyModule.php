<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Container;
use Dagger\File;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    // Return a directory
    public function getDir(): Directory {
        return $this->base()
            ->directory('/src');
    }

    #[DaggerFunction]
    // Return a file
    public function getFile(): File {
        return $this->base()
            ->file('/src/foo');
    }

    #[DaggerFunction]
    // Return a base container
    public function base(): Container {
        return dag()
            ->container()
            ->from('alpine:latest')
            ->withExec(["mkdir", "/src"])
		        ->withExec(["touch", "/src/foo", "/src/bar"]);
    }
}
