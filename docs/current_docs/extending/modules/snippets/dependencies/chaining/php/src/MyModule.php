<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction, ListOfType};
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function example(
        Directory $buildSrc,
        #[ListOfType('string')]
        array $buildArgs,
    ): Directory {
        return dag()
            ->golang()
            ->build(source: $buildSrc, args: $buildArgs)
            ->terminal();
    }
}
