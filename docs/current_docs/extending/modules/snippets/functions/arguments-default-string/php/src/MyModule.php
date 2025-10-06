<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerFunction, DaggerObject};

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function hello(string $name = 'world'): string
    {
        return "Hello, {$name}";
    }
}
