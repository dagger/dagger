<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Container;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function clone(string $repository, string $branch): Container
    {
        $r = dag()->git($repository);
        $d = dag()->directory();
        $d = $r->branch($branch)->tree();

        return dag()
            ->container()
            ->from('alpine:latest')
            ->withDirectory('/src', $d);
    }
}
