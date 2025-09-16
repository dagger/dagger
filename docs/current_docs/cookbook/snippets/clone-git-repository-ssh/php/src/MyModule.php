<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Container;
use Dagger\Socket;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    // Demonstrates an SSH-based clone requiring a user-supplied sshAuthSocket.
    #[DaggerFunction]
    public function cloneWithSsh(string $repository, string $ref, Socket $sock): Container
    {

        $repoDir = dag()
            ->git($repository, true, '', $sock)
            ->ref($ref)
            ->tree();

        return dag()
            ->container()
            ->from('alpine:latest')
            ->withDirectory('/src', $repoDir)
            ->withWorkdir('/src');
    }
}
