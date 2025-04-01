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
        $workspace = dag()->toyWorkspace();
        $environment = dag()->env()
            ->withToyWorkspaceInput("before", $workspace, "these are the tools to complete the task")
            ->withStringInput("assignment", $assignment, "this is the assignment, complete it")
            ->withToyWorkspaceOutput("after", "the ToyWorkspace with the completed assignment");
        return dag()
            ->llm()
            ->withEnv($environment)
            ->withPrompt("
            You are an expert go programmer. You have access to a workspace.
			Use the default directory in the workspace.
			Do not stop until the code builds.")
            ->env()
            ->output("after")
            ->asToyWorkspace()
            ->container();
    }
}
