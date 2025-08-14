<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction};

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function hello(bool $shout): string
    {
        $message = 'Hello, world';

        if ($shout) {
            return strtoupper($message);
        }

        return $message;
    }
}
