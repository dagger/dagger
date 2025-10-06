<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerFunction, DaggerObject};

#[DaggerObject]
class ValueManipulated
{
    #[DaggerFunction]
    public function __construct(
        private bool $arg,
    ) {
        $this->arg = ! $this->arg;
    }

    #[DaggerFunction]
    public function getConstructorArg(): bool
    {
        return $this->arg;
    }
}
