<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction};

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function __construct(
        private readonly string $greeting = 'Hello',
        private readonly string $name = 'World',
    ) {
    }

    #[DaggerFunction]
    public function message(): string
    {
        return "{$this->greeting} {$this->message}";
    }
}
