<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\Argument;
use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Container;
use Dagger\Directory;
use GraphQL\Exception\QueryError;

use function Dagger\dag;

#[DaggerObject]
final class PhpSdkDev
{
    private const SDK_ROOT='/src/sdk/php';

    #[DaggerFunction('Run tests from source directory')]
    public function test(
        #[Argument('Run tests from the given source directory')]
        Directory $source,
        #[Argument('Only run tests in the given group')]
        ?string $group = null,
    ): Container {
        return $this->base($source)->withExec(
            is_null($group) ? ['phpunit'] : ['phpunit', "--group=$group"],
            experimentalPrivilegedNesting: true,
        );
    }

    #[DaggerFunction('Lint the source directory')]
    public function lint(Directory $source): Container {
        return $this->base($source)->withExec(['phpcs']);
    }

    /**
     * PHPCBF exit codes:
     * 0: No errors were found
     * 1: Errors were found, all errors were fixed
     * 2: Errors were found, not all errors could be fixed
     * 3: General script execution error occurred
     *
     * All but exit code 3 are successful executions of the formatter.
     * These exit codes cannot be customised in configuration:
     * https://github.com/squizlabs/PHP_CodeSniffer/issues/1818#issuecomment-354420927
     *
     * @TODO simplify this script if/when phpcbf can customise exit codes.
     * This will most likely occur on a 4.0 release:
     * https://github.com/PHPCSStandards/PHP_CodeSniffer/issues/184
     */
    #[DaggerFunction('Format the source directory')]
    public function format(Directory $source): Directory
    {
        $result = dag()->alwaysExec()->exec($this->base($source), ['phpcbf']);

        if (dag()->alwaysExec()->lastExitCode($result) === '3') {
            throw new QueryError(['errors' => [[
                'message' => 'An error occured during execution of PHPCBF',
            ]]]);
        }

        $original = $this->base($source)->directory(self::SDK_ROOT);
        return $original->diff($result->directory(self::SDK_ROOT));
    }

    #[DaggerFunction('Format the source directory')]
    public function formatStdout(Directory $source): string
    {
        $result = dag()->alwaysExec()->exec($this->base($source), ['phpcbf']);

        if (dag()->alwaysExec()->lastExitCode($result) === '3') {
            throw new QueryError(['errors' => [[
                'message' => 'An error occured during execution of PHPCBF',
            ]]]);
        }

        return dag()->alwaysExec()->stdout($result);
    }

    private function base(Directory $source): Container
    {
        return dag()
            ->container()
            ->from('php:8.3-cli-alpine')
            ->withFile('/usr/bin/composer', dag()
                ->container()
                ->from('composer:2')
                ->file('/usr/bin/composer'))
            ->withMountedCache('/root/.composer', dag()
                ->cacheVolume('composer-php:8.3-cli-alpine'))
            ->withEnvVariable('COMPOSER_HOME', '/root/.composer')
            ->withEnvVariable('COMPOSER_ALLOW_SUPERUSER', '1')
            ->withMountedDirectory(self::SDK_ROOT, $source)
            ->withWorkdir(self::SDK_ROOT)
            ->withExec(['composer', 'install'])
            ->withEnvVariable('PATH', './vendor/bin:$PATH', expand: true);
    }
}
