<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;

#[DaggerObject]
#[Doc('Priority level')]
enum Priority: int
{
    #[Doc('Low priority')]
    case Low = 1;

    #[Doc('Medium priority')]
    case Medium = 2;

    #[Doc('High priority')]
    case High = 3;
}
