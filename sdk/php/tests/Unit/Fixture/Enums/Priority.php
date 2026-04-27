<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture\Enums;

use Dagger\Attribute\Doc;

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
