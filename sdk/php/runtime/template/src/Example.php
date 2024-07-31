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
     #[DaggerFunction('Echo the value to standard output')]
     public function echo(string $value): Container
     {
         return dag()
             ->container()
             ->from('alpine:latest')
             ->withExec(['echo', $value]);
     }

    #[DaggerFunction('Search a directory for lines matching a pattern')]
     public function grepDir(
         #[Argument('The directory to search')]
         Directory $directory,
         #[Argument('The pattern to search for')]
         string $pattern
    ): string {
         return dag()
             ->container()
             ->from('alpine:latest')
             ->withMountedDirectory('/mnt', $directory)
             ->withWorkdir('/mnt')
             ->withExec(["grep", '-R', $pattern, '.'])
             ->stdout();
     }
}
