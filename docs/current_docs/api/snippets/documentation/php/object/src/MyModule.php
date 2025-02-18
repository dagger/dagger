<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction, Doc};

#[DaggerObject]
#[Doc('The object represents a single user of the system.')]
class MyModule
{
    #[DaggerFunction]
    public function __construct(
        #[Doc('The name of the user.')]
        private string $name,
        #[Doc('The age of the user.')]
        private int $age,
    ) {
    }
}
