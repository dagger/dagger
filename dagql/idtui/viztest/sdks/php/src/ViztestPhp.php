<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\Clients\SdkDep\Client as SdkDep;
use Dagger\Container;
use GraphQL\Exception\QueryError;

use function Dagger\core;

/**
 * The external PHP SDK's fixture for the golden tests: every SDK fixture has
 * the same functions, so their goldens line up.
 *
 * The PHP SDK has no function cache policy, so these functions are cached
 * by default.
 */
#[DaggerObject]
class ViztestPhp
{
    #[DaggerFunction]
    public function __construct()
    {
    }

    #[DaggerFunction]
    #[Doc('Logs to stdout and stderr, and succeeds')]
    public function logSuccess(): string
    {
        $this->logBoth();
        return 'php function result';
    }

    #[DaggerFunction]
    #[Doc('Logs to stdout and stderr, then throws an error of its own')]
    public function failNative(): void
    {
        $this->logBoth();
        throw new \RuntimeException('php function failed natively');
    }

    #[DaggerFunction]
    #[Doc('Logs to stdout and stderr, then exits the process with a custom code')]
    public function failExitCode(): void
    {
        $this->logBoth();
        exit(3);
    }

    #[DaggerFunction]
    #[Doc('Runs a command that fails, and lets its error propagate')]
    public function failExec(): string
    {
        return $this->failingExec()->stdout();
    }

    #[DaggerFunction]
    #[Doc('Runs a command that fails, and wraps its error')]
    public function failExecWrapped(): string
    {
        try {
            return $this->failingExec()->stdout();
        } catch (QueryError $e) {
            throw new \RuntimeException('php function ran a failing command: ' . $e->getMessage(), previous: $e);
        }
    }

    #[DaggerFunction]
    #[Doc('Logs a line, then reads a file that doesn\'t exist, and lets the error propagate')]
    public function failApi(): string
    {
        fwrite(STDOUT, "php function stdout\n");
        fflush(STDOUT);
        return core()->directory()->file('doesnt-exist.txt')->contents();
    }

    #[DaggerFunction]
    #[Doc('Calls a function of a dependency whose command fails')]
    public function callFailingDep(): string
    {
        return SdkDep::sdkDep()->failExec();
    }

    #[DaggerFunction]
    #[Doc('Logs a line, then calls a function of a dependency whose command fails')]
    public function callFailingDepLogged(): string
    {
        fwrite(STDOUT, "php function stdout\n");
        fflush(STDOUT);
        return SdkDep::sdkDep()->failExec();
    }

    private function failingExec(): Container
    {
        return core()
            ->container()
            ->from('alpine')
            ->withExec(['sh', '-c', 'echo exec stdout; sleep 0.1; echo exec stderr >&2; exit 1']);
    }

    /**
     * Logs a line to stdout, then one to stderr. The streams are captured
     * apart, so the pause keeps their order stable in the goldens.
     */
    private function logBoth(): void
    {
        fwrite(STDOUT, "php function stdout\n");
        fflush(STDOUT);
        usleep(100000);
        fwrite(STDERR, "php function stderr\n");
        fflush(STDERR);
    }
}
