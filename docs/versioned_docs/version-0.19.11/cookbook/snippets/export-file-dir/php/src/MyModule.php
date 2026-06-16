<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\Container;
use Dagger\File;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Return a directory')]
    public function getDir(): Directory {
        return $this->base()
            ->directory('/src');
    }

    #[DaggerFunction]
    #[Doc('Return a file')]
    public function getFile(): File {
        return $this->base()
            ->file('/src/foo');
    }

    #[DaggerFunction]
    #[Doc('Return a base container')]
    public function base(): Container {
        return dag()
            ->container()
            ->from('alpine:latest')
            ->withExec(["mkdir", "/src"])
		        ->withExec(["touch", "/src/foo", "/src/bar"]);
    }
}
