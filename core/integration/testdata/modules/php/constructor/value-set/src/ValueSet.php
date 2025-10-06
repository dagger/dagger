<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerFunction, DaggerObject};

#[DaggerObject]
class ValueSet
{
    #[DaggerFunction]
    public function __construct(
        private readonly string $arg,
    ) {
    }

    #[DaggerFunction]
    public function getConstructorArg(): string
    {
        return $this->arg;
    }
}
