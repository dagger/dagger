<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerFunction, DaggerObject};
use Dagger\{File, Directory};

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function build(
        Directory $src,
        string $arch,
        string $os,
    ): File {
        return dag()
            ->container()
            ->from('golang:1.21')
            ->withMountedDirectory('/src', $src)
            ->withWorkdir('/src')
            ->withEnvVariable('GOARCH', $arch)
            ->withEnvVariable('GOOS', $os)
            ->withEnvVariable('CGO_ENABLED', '0')
            ->withExec(['go', 'build', '-o', 'build/'])
            ->file('/src/build/hello');
    }
}
