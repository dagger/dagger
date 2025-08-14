<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction};
use Dagger\Secret;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function githubApi(Secret $token): string
    {
        return dag()
            ->container()
            ->from('alpine:3.17')
            ->withSecretVariable('GITHUB_API_TOKEN', $token)
            ->withExec(['apk', 'add', 'curl'])
            ->withExec([
                'sh',
                '-c',
                'curl "https://api.github.com/repos/dagger/dagger/issues"'
                . ' --header "Authorization: Bearer $GITHUB_API_TOKEN"'
                . ' --header "Accept: application/vnd.github+json"',
            ])
            ->stdout();
    }
}
