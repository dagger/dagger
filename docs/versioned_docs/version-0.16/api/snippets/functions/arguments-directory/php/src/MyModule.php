<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction, ListOfType};

use Dagger\Directory;
use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function tree(Directory $src, string $depth): string
    {
        return dag()
            ->container()
            ->from('alpine:latest')
            ->withMountedDirectory('/mnt', $src)
            ->withWorkdir('/mnt')
            ->withExec(['apk', 'add', 'tree'])
            ->withExec(['tree', '-L', $depth])
            ->stdout();
    }
}
