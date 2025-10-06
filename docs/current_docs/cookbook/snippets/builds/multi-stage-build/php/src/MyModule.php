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
    // Build and publish Docker container
    #[DaggerFunction]
    public function build(Directory $src): string
    {
        // build app
        $builder = dag()
            ->container()
            ->from('golang:latest')
            ->withDirectory('/src', $src)
            ->withWorkdir('/src')
            ->withEnvVariable('CGO_ENABLED', '0')
            ->withExec(['go', 'build', '-o', 'myapp']);

        // publish binary on alpine base
        $prodImage = dag()
            ->container()
            ->from('alpine')
            ->withFile('/bin/myapp', $builder->file('/src/myapp'))
            ->withEntrypoint(['/bin/myapp']);

        // publish to ttl.sh registry
        $addr = $prodImage->publish('ttl.sh/myapp:latest');
        return $addr;

    }
}
