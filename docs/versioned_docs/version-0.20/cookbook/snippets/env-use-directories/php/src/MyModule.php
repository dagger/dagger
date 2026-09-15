<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function agent(): Directory {
        $dir = dag()
            ->git('github.com/dagger/dagger')
            ->branch('main')
            ->tree();

        $environment = dag()
            ->env()
            ->withDirectoryInput('source', $dir, 'the source directory to use')
            ->withDirectoryOutput('result', 'the updated directory');

        $work = dag()
            ->llm()
            ->withEnv($environment)
            ->withPrompt(<<<'PROMPT'
                You have access to a directory containing various files.
                Translate only the README file in the directory to French and Spanish.
                Ensure that the translations are accurate and maintain the original formatting.
                Do not modify any other files in the directory.
                Create a sub-directory named 'translations' to store the translated files.
                For French, add an 'fr' suffix to the translated file name.
                For Spanish, add an 'es' suffix to the translated file name.
                Do not create any other new files or directories.
                Do not delete any files or directories.
                Do not investigate any sub-directories.
                Once complete, return the 'translations' directory.
                PROMPT);

        return $work
            ->env()
            ->output('result')
            ->asDirectory();
    }
}
