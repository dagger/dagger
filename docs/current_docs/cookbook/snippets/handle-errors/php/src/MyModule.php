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
    public function test(): string {
        try {
            return dag()
                ->container()
                ->from('alpine')
                // ERROR: cat: read error: Is a directory
                ->withExec(['cat', '/'])
                ->stdout();
        } catch (\Exception $e) {
            return 'Test pipeline failure: ' . $e->stderr();
        }
    }
}
