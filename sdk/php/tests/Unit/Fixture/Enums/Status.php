<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture\Enums;

use Dagger\Attribute\Doc;

#[Doc('The status of a task')]
enum Status: string
{
    #[Doc('Task is active')]
    case Active = 'active';

    #[Doc('Task is inactive')]
    case Inactive = 'inactive';

    case Pending = 'pending';
}
