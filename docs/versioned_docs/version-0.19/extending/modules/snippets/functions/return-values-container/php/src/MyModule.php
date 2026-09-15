<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction, ListOfType};
use Dagger\Container;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function alpineBuilder(
        #[ListOfType('string')]
        array $packages,
    ): Container {
        $ctr = dag()->container()->from('alpine:latest');
        foreach ($packages as $pkg) {
            $ctr = $ctr->withExec(['apk', 'add', $pkg]);
        }
        return $ctr;
    }
}
