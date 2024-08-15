<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\Argument;
use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Container;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
class Example
{
     #[DaggerFunction('Returns a container that echoes whatever string argument is provided')]
     public function containerEcho(string $stringArg): Container
     {
         return dag()
             ->container()
             ->from('alpine:latest')
             ->withExec(['echo', $stringArg]);
     }

    #[DaggerFunction('Returns lines that match a pattern in the files of the provided Directory')]
     public function grepDir(
         #[Argument('The directory to search')]
         Directory $directoryArg,
         #[Argument('The pattern to search for')]
         string $pattern
    ): string {
         return dag()
             ->container()
             ->from('alpine:latest')
             ->withMountedDirectory('/mnt', $directoryArg)
             ->withWorkdir('/mnt')
             ->withExec(["grep", '-R', $pattern, '.'])
             ->stdout();
     }
}
