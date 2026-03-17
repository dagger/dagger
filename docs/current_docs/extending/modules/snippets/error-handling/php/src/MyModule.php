<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction};

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function divide(int $a, int $b): float
    {
        if ($b === 0) {
            throw new \RuntimeException('Cannot divide by zero');
        }

        return $a / $b;
    }
}
