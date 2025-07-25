<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction, ListOfType};
use Dagger\File;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function readFile(File $source): string
    {
        return dag()
            ->container()
            ->from('alpine:latest')
            ->withFile('/src/myfile', $source)
            ->withExec(['cat', '/src/myfile'])
            ->stdout();
    }
}
