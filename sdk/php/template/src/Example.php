<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Client;
use Dagger\Container;
use Dagger\Directory;

#[DaggerObject]
class Example
{
    public Client $client;

     #[DaggerFunction]
     public function echo(string $value): Container
     {
         return $this->client->container()->from('alpine:latest')
             ->withExec(['echo', $value]);
     }

    #[DaggerFunction]
     public function grepDir(Directory $directory, string $pattern): string
     {
         return $this->client->container()->from('alpine:latest')
             ->withMountedDirectory('/mnt', $directory)
             ->withWorkdir('/mnt')
             ->withExec(["grep", '-R', $pattern, '.'])
             ->stdout();
     }
}
