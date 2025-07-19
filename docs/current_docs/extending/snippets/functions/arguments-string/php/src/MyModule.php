<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction};

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function getUser(string $gender): string
    {
        return dag()
            ->container()
            ->from("alpine:latest")
            ->withExec(["apk", "add", "curl"])
            ->withExec(["apk", "add", "jq"])
            ->withExec([
                "sh",
                "-c",
                "curl https://randomuser.me/api/?gender={$gender} | jq .results[0].name",
            ])
            ->stdout();
    }
}
