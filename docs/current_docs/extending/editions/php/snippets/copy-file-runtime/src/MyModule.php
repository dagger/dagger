<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\File;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function copyFile(
        #[Doc('source file')]
        File $source
    ): void {
        $source->export('foo.txt');
        // your custom logic here
        // for example, read and print the file in the Dagger Engine container
        echo file_get_contents('foo.txt');
    }
}
