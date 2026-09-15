<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\Check;
use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Container;

use function Dagger\dag;

#[DaggerObject]
class Checks
{
    #[DaggerFunction, Check]
    public function passing(): void {}

    #[DaggerFunction, Check]
    public function failing(): void
    {
        throw new \RuntimeException('failed');
    }

    #[DaggerFunction, Check]
    public function passingContainer(): Container
    {
        return dag()
            ->container()
            ->from('alpine:3')
            ->withExec(['sh', '-c', 'exit 0']);
    }

    #[DaggerFunction, Check]
    public function failingContainer(): Container
    {
        return dag()
            ->container()
            ->from('alpine:3')
            ->withExec(['sh', '-c', 'exit 1']);
    }
}
