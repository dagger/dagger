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
    public function clone(string $repository, string $locator, string $ref): Container
    {
        $r = dag()->git($repository);
        $d = dag()->directory();

        switch ($locator) {
            case 'branch':
                $d = $r->branch($ref)->tree();
                break;
            case 'tag':
                $d = $r->tag($ref)->tree();
                break;
            default:
                $d = $r->commit($ref)->tree();
                break;
        }

        return dag()
            ->container()
            ->from('alpine:latest')
            ->withDirectory('/src', $d);
    }
}
