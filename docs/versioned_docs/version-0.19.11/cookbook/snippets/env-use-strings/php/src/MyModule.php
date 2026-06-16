<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function agent(
        string $question,
    ): string {
        $environment = dag()
            ->env()
            ->withStringInput('question', $question, 'the question')
            ->withStringOutput('answer', 'the answer to the question');

        $work = dag()
            ->llm()
            ->withEnv($environment)
            ->withPrompt(<<<PROMPT
                You are an assistant that helps with complex questions.
                You will receive a question and you need to provide a detailed answer.
                Make sure to use the provided context and environment variables.
                Your answer should be clear and concise.
                Your question is: $question
                PROMPT);

        return $work
            ->env()
            ->output('answer')
            ->asString();
    }
}
