<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    // define build matrix
    const GOOSES = ['linux', 'darwin'];
    const GOARCHES = ['amd64', 'arm64'];

    // Build and return directory of go binaries
    #[DaggerFunction]
    public function build(Directory $src): Directory
    {
        // create empty directory to put build artifacts
        $outputs = dag()->directory();

        $golang = dag()
            ->container()
            ->from('golang:latest')
            ->withDirectory('/src', $src)
            ->withWorkdir('/src');

        foreach (self::GOOSES as $goos) {
          foreach (self::GOARCHES as $goarch) {
            // create a directory for each OS and architecture
            $path = "build/$goos/$goarch/";

            // build artifact
            $build = $golang
                ->withEnvVariable('GOOS', $goos)
                ->withEnvVariable('GOARCH', $goarch)
                ->withExec(['go', 'build', '-o', $path]);

            // add build to outputs
            $outputs = $outputs->withDirectory($path, $build->directory($path));
          }
        }

        return $outputs;
    }
}
