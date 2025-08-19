<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\Container;

use function Dagger\dag;

#[DaggerObject]
#[Doc('A generated module for CodingAgent functions')]
class CodingAgent
{
    #[DaggerFunction]
    #[Doc('Write a Go program')]
    public function goProgram(string $assignment): Container
    {
        $environment = dag()->env()
            ->withStringInput('assignment', $assignment, 'the assignment to complete')
            ->withContainerInput('builder', dag()->container()->from('golang')->withWorkdir('/app'), 'a container to use for building Go code')
            ->withContainerOutput('completed', 'the completed assignment in the Golang container');
        return dag()
            ->llm()
            ->withEnv($environment)
            ->withPrompt('
                You are an expert Go programmer with an assignment to create a Go program
                Create files in the default directory in $builder
                Always build the code to make sure it is valid
                Do not stop until your assignment is completed and the code builds
                Your assignment is: $assignment')
            ->env()
            ->output('completed')
            ->asContainer();
    }
}
