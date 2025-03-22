<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerFunction, DaggerObject};

#[DaggerObject] class VoidKind
{
    #[DaggerFunction] public function getVoid(): void {}

    #[DaggerFunction] public function giveAndGetNull(null $arg): null
    {
        return $arg;
    }
}
