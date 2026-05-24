<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;

#[DaggerObject]
#[Doc('The status of a task')]
enum Task: string
{
    #[Doc('Task is active')]
    case Todo = 'todo';

    #[Doc('Task is done')]
    case Done = 'done';
}
