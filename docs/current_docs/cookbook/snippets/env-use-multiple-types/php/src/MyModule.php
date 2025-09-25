<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\File;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function agent(): File {
        $dir = dag()
            ->git('github.com/golang/example')
            ->branch('master')
            ->tree();

        $builder = dag()
            ->container()
            ->from('golang:latest');

        $environment = dag()
            ->env()
            ->withContainerInput('container', $builder, 'a Golang container')
            ->withDirectoryInput('directory', $dir, 'a directory with source code')
            ->withFileOutput('file', 'the built Go executable');

        $work = dag()
            ->llm()
            ->withEnv($environment)
            ->withPrompt(<<<'PROMPT'
                You have access to a Golang container.
                You also have access to a directory containing Go source code.
                Mount the directory into the container and build the Go application.
                Once complete, return only the built binary.
                PROMPT);

        return $work
            ->env()
            ->output('file')
            ->asFile();
    }
}
