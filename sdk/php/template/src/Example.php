<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerArgument;
use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Client;
use Dagger\Container;
use Dagger\Directory;

#[DaggerObject]
class Example
{
    public Client $client;

     #[DaggerFunction('Echo the value to standard output')]
     public function echo(
         #[DaggerArgument('The value to echo')]
         string $value
     ): Container {
         return $this->client->container()->from('alpine:latest')
             ->withExec(['echo', $value]);
     }

    #[DaggerFunction('Search a directory for lines matching a pattern')]
     public function grepDir(
         #[DaggerArgument('The directory to search')]
         Directory $directory,
         #[DaggerArgument('The pattern to search for')]
         string $pattern
    ): string {
         return $this->client->container()->from('alpine:latest')
             ->withMountedDirectory('/mnt', $directory)
             ->withWorkdir('/mnt')
             ->withExec(["grep", '-R', $pattern, '.'])
             ->stdout();
     }
}
