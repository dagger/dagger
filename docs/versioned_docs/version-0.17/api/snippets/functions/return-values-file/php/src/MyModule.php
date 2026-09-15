<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction};
use Dagger\{Directory, File};

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function archiver(Directory $src): File
    {
        return dag()
            ->container()
            ->from('alpine:latest')
            ->withExec(['apk', 'add', 'zip'])
            ->withMountedDirectory('/src', $src)
            ->withWorkdir('/src')
            ->withExec(['sh', '-c', 'zip -p -r out.zip *.*'])
            ->file('/src/out.zip');
    }
}
