<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\Container;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
#[Doc('A generated module for Defaults functions')]
class Defaults
{
    #[DaggerFunction]
    public function echo(string $value = 'default value'): string
    {
        return $value;
    }
}
