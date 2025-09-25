<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction, Doc};

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('The greeting to use')]
    public $greeting;

    #[Doc('Who to greet')]
    private $name;

    #[DaggerFunction]
    public function __construct(
        #[Doc('The greeting to use')]
        string $greeting = 'Hello',
        #[Doc('Who to greet')]
        string $name = 'World',
    ) {
        $this->greeting = $greeting;
        $this->name = $name;
    }

    #[DaggerFunction]
    #[Doc('Return the greeting message')]
    public function message(): string
    {
        return "{$this->greeting}, {$this->message}";
    }
}
