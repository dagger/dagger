<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerFunction, DaggerObject};

#[DaggerObject] class ScalarKind
{
    #[DaggerFunction] public bool $boolField = true;
    #[DaggerFunction] public float $floatField = 3.14;
    #[DaggerFunction] public int $intField = 1;
    #[DaggerFunction] public string $stringField = 'Hello, field!';

    #[DaggerFunction] public function setFields(): ScalarKind
    {
        $this->boolField = false;
        $this->floatField = 1.618;
        $this->intField = 2;
        $this->stringField = 'HOWDY, FIELD!';
        return $this;
    }

    #[DaggerFunction] public function oppositeBool(bool $arg): bool
    {
        return !$arg;
    }

    #[DaggerFunction] public function halfFloat(float $arg): float
    {
        return $arg / 2;
    }

    #[DaggerFunction] public function doubleInt(int $arg): int
    {
        return $arg * 2;
    }

    #[DaggerFunction] public function capitalizeString(string $arg): string
    {
        return ucwords($arg);
    }
}
