<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction};

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function addFloat(float $a, float $b): float
    {
        return $a + $b;
    }
}
