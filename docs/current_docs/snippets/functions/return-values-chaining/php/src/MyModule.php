<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction};

#[DaggerObject]
class MyModule
{
    private string $greeting = 'Hello';
    private string $name = 'World';

    #[DaggerFunction]
    public function withGreeting(string $greeting): MyModule
    {
        $this->greeting = $greeting;
        return $this;
    }

    #[DaggerFunction]
    public function withName(string $name): MyModule
    {
        $this->name = $name;
        return $this;
    }

    #[DaggerFunction]
    public function message(): string
    {
        return "{$this->greeting} {$this->name}";
    }
}
