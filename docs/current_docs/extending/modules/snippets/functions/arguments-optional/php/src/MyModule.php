<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerFunction, DaggerObject};

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function hello(?string $name): string
    {
        if (isset($name)) {
            return "Hello, {$name}";
        }
        return 'Hello, world';
    }
}
