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
        return dag()
            ->llm()
            ->withToyWorkspace(dag()->toyWorkspace())
            ->withPromptVar("assignment", $assignment)
            ->withPrompt("
            You are an expert go programmer. You have access to a workspace.
			Use the default directory in the workspace.
			Do not stop until the code builds.
			Do not use the container.
			Complete the assignment: $assignment")
            ->toyWorkspace()
            ->container();
    }
}
