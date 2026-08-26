<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\Container;
use Dagger\Socket;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Demonstrates an SSH-based clone requiring a user-supplied sshAuthSocket.')]
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
