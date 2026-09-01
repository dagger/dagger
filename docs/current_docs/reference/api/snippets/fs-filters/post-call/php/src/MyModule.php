<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerFunction, DaggerObject};
use Dagger\{Container, Directory};

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function foo(Directory $source): Container
    {
        $builder = dag()
            ->container()
            ->from('golang:latest')
            ->withDirectory('/src', $source, exclude: ['*.git', 'internal'])
            ->withWorkdir('/src/hello')
            ->withExec(['go', 'build', '-o', 'hello.bin', '.']);

        return dag()
            ->container()
            ->from('alpine:latest')
            ->withDirectory('/app', $builder->directory('/src/hello'), include: ['hello.bin'])
            ->withEntrypoint(['/app/hello.bin']);
    }
}
