<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Secret;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function showSecret(Secret $token): string
    {
        return dag()
            ->container()
            ->from('alpine:latest')
            ->withSecretVariable('MY_SECRET', $token)
            ->withExec(['sh', '-c', 'echo "this is the secret: $MY_SECRET"'])
            ->stdout();
    }
}
