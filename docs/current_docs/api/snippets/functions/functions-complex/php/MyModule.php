<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function getUser(): string
    {
        return dag()
            ->container()
            ->from('alpine:latest')
            ->withExec(['apk', 'add', 'curl'])
            ->withExec(['apk', 'add', 'jq'])
            ->withExec([
                'sh',
                '-c',
                'curl https://randomuser.me/api/ | jq .results[0].name',
            ])
            ->stdout();
    }
}
