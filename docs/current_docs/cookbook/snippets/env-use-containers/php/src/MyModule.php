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
    public function agent(): Container {
        $base = dag()
            ->container()
            ->from('alpine:latest');

        $environment = dag()
            ->env()
            ->withContainerInput('base', $base, 'a base container to use')
            ->withContainerOutput('result', 'the updated container');

        $work = dag()
            ->llm()
            ->withEnv($environment)
            ->withPrompt(<<<'PROMPT'
                You are a software engineer with deep knowledge of Web application development.
                You have access to a container.
                Install the necessary tools and libraries to create a
                complete development environment for Web applications.
                Once complete, return the updated container.
                PROMPT);

        return $work
            ->env()
            ->output('result')
            ->asContainer();
    }
}
