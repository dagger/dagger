<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\DefaultPath;
use Dagger\Attribute\Doc;
use Dagger\Container;
use Dagger\Directory;
use Dagger\ReturnType;
use GraphQL\Exception\QueryError;

use function Dagger\dag;

#[DaggerObject]
#[Doc('The PHP SDK\'s development module.')]
final class PhpSdkDev
{
    private const SDK_ROOT = '/src/sdk/php';

    #[DaggerFunction]
    public function __construct(
        #[DefaultPath('..')]
        #[Doc('The PHP SDK source directory.')]
        private Directory $source,
        private ?Container $container = null
    ) {
        if (is_null($this->container)) {
            $this->container = dag()
                ->container()
                ->from('php:8.3-cli-alpine')
                ->withFile('/usr/bin/composer', dag()
                    ->container()
                    ->from('composer:2')
                    ->file('/usr/bin/composer'))
                ->withMountedCache('/root/.composer', dag()
                    ->cacheVolume('composer-php:8.3-cli-alpine'))
                ->withEnvVariable('COMPOSER_HOME', '/root/.composer')
                ->withEnvVariable('COMPOSER_NO_INTERACTION', '1')
                ->withEnvVariable('COMPOSER_ALLOW_SUPERUSER', '1');
        }

        $this->container = $this->container
            ->withMountedDirectory(self::SDK_ROOT, $this->source)
            ->withWorkdir(self::SDK_ROOT)
            ->withExec(['composer', 'install'])
            ->withEnvVariable('PATH', './vendor/bin:$PATH', expand: true);
    }

    #[DaggerFunction]
    #[Doc('Run tests in source directory')]
    public function test(
        #[Doc('Only run tests in the given group')]
        ?string $group = null
    ): Container {
        return $this->container->withExec(
            is_null($group) ? ['phpunit'] : ['phpunit', "--group=$group"]
        );
    }

    #[DaggerFunction]
    #[Doc('Run linter in source directory')]
    public function lint(): Container
    {
        return $this->container->withExec(['phpcs']);
    }

    #[DaggerFunction]
    #[Doc('Run static analysis in source directory')]
    public function analyze(): Container
    {
        return $this->container->withExec([
            'phpstan',
            '--no-progress',
            '--memory-limit=1G',
        ]);
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
    #[DaggerFunction]
    #[Doc('Return diff from formatting source directory')]
    public function format(): Directory
    {
        $result = $this->container->withExec(
            args: ['phpcbf'],
            expect: ReturnType::ANY
        );

        if (!in_array($result->exitCode(), [0, 1, 2], true)) {
            throw new QueryError([
                'errors' => [
                    [
                        'message' =>
                            'An error occured during execution of PHPCBF',
                    ],
                ],
            ]);
        }

        $original = $this->container->directory(self::SDK_ROOT);

        return $original->diff($result->directory(self::SDK_ROOT));
    }

    #[DaggerFunction]
    #[Doc('PHP SDK Dev base container')]
    public function base(): Container
    {
        return $this->container;
    }

    #[DaggerFunction]
    #[Doc('Return stdout from formatting source directory')]
    public function formatStdout(): string
    {
        return $this->container
            ->withExec(args: ['phpcbf'], expect: ReturnType::ANY)
            ->stdout();
    }
}
