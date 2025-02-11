<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerFunction, DaggerObject, DefaultPath, ReturnsListOfType};
use Dagger\Directory;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[ReturnsListOfType('string')]
    public function readDir(
        #[DefaultPath('.')]
        Directory $source,
    ): array {
        return $source->entries();
    }
}
