<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;

#[DaggerObject]
#[Doc('A level, with no backing type')]
enum Level
{
    #[Doc('Not much')]
    case Low;

    #[Doc('Quite a lot')]
    case High;
}
