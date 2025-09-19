<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\Directory;
use Dagger\Platform;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Build and publish multi-platform image')]
    public function build(Directory $src): string
    {
        // platforms to build for and push in a multi-platform image
        $platforms = [
          new Platform('linux/amd64'), // a.k.a. x86_64
          new Platform('linux/arm64'), // a.k.a. arch64
          new Platform('linux/s390x'), // a.k.a. IBM S/390
        ];

        // container registry for multi-platform image
        $imageRepo = 'ttl.sh/myapp:latest';
        $platformVariants = [];
        foreach ($platforms as $platform) {
            // parse architecture using containerd utility module
            $platformArch = dag()
                ->containerd()
                ->architectureOf($platform);

            $ctr = dag()
                ->container($platform)
                ->from('golang:1.21-alpine')
                // mount source
                ->withDirectory('/src', $src)
                // mount empty dir where built binary will live
                ->withDirectory('/output', dag()->directory())
                // ensure binary will be statically linked and thus executable
                // in the final image
                ->withEnvVariable('CGO_ENABLED', '0')
                // configure go compiler to use cross-compilation targeting the
                // desired platform
                ->withEnvVariable('GOOS', 'linux')
                ->withEnvVariable('GOARCH', $platformArch)
                ->withWorkdir('/src')
                ->withExec(['go', 'build', '-o', '/output/hello']);

            // select output directory
            $outputDir = $ctr->directory('/output');

            // wrap output directory in a new empty container marked
            // with the same platform
            $binaryCtr = dag()
                ->container($platform)
                ->withRootfs($outputDir);

            array_push($platformVariants, $binaryCtr);
        }

        // publish to registry
        $imageDigest = dag()
            ->container()
            ->publish($imageRepo, $platformVariants);

        return $imageDigest;
    }
}
