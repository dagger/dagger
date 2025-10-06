<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction, Doc};

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function __construct(
        #[DaggerFunction]
        #[Doc('The greeting to use')]
        public string $greeting = 'Hello',
        #[Doc('Who to greet')]
        private string $name = 'World',
    ) {}

    #[DaggerFunction]
    #[Doc('Return the greeting message')]
    public function message(): string
    {
        return "{$this->greeting}, {$this->message}";
    }
}
