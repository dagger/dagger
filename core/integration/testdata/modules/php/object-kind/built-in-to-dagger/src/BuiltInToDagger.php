<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerFunction, DaggerObject};
use Dagger\{Directory, File};

use function Dagger\dag;

#[DaggerObject]
class BuiltInToDagger
{
    #[DaggerFunction] public function capitalizeContents(File $arg): File
    {
        return dag()
            ->directory()
            ->withNewFile('/foo', ucwords($arg->contents()))
            ->file('/foo');
    }

    #[DaggerFunction] public function withBaz(Directory $arg): Directory
    {
        return dag()->container()
            ->withDirectory('/foo', $arg)
            ->withNewFile('/foo/baz', 'Howdy, Planet!')
            ->directory('/foo');
    }
}
