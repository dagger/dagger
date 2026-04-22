<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction, Doc};

#[DaggerObject]
#[Doc('A simple example module to say hello.')]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Return a greeting')]
    public function hello(
        #[Doc('Who to greet')]
        string $name,
        #[Doc('The greeting to display')]
        string $greeting
    ): string {
        return "{$greeting} {$name}";
    }

    #[DaggerFunction]
    #[Doc('Return a loud greeting')]
    public function loudHello(
        #[Doc('Who to greet')]
        string $name,
        #[Doc('The greeting to display')]
        string $greeting
    ): string {
        return strtoupper("{$greeting} {$name}");
    }
}
