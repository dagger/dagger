<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction, ListOfType};

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function hello(#[ListOfType('string')]array $names): string
    {
        $message = 'Hello';

        if (!empty($names)) {
            $message .= " " . implode(', ', $names);
        }

        return $message;
    }
}
