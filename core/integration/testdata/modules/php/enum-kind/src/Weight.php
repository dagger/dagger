<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;

#[DaggerObject]
#[Doc('A weight, backed by integers')]
enum Weight: int
{
    #[Doc('Barely there')]
    case Light = 1;

    #[Doc('Rather heavy')]
    case Heavy = 10;
}
