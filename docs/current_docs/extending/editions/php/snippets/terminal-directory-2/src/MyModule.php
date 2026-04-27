<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\Container;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function advancedDirectory(): string {
        return dag()
            ->git('https://github.com/dagger/dagger.git')
            ->head()
            ->tree()
            ->terminal(
                container: dag()->container()->from('ubuntu'),
                cmd: ['/bin/bash'],
            )
            ->file('README.md')
            ->contents();
    }
}
