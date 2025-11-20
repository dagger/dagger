<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\Secret;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Query the GitHub API')]
    public function githubAuth(
        #[Doc('GitHub Hosts configuration File')]
        Secret $ghCreds,
    ): string {
        return dag()
          ->container()
          ->from('alpine:3.17')
          ->withExec(['apk', 'add', 'github-cli'])
          ->withMountedSecret('/root/.config/gh/hosts.yml', $ghCreds)
          ->withExec(['gh', 'auth', 'status'])
          ->stdout();
    }
}
