<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\DefaultPath;
use Dagger\Attribute\Doc;
use Dagger\Container;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
class HelloDagger
{
    #[DaggerFunction]
    #[Doc('Return the result of running unit tests')]
    public function test(
      #[DefaultPath('/')]
      Directory $source,
    ): string {
        return $this
            // get the build environment container
            // by calling another Dagger Function
            ->buildEnv($source)
            // call the test runner
            ->withExec(['npm', 'run', 'test:unit', 'run'])
            // capture and return the command output
            ->stdout();
    }
}
