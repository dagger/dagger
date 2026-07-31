<?php

namespace Dagger\Connection;

use Dagger\Connection;
use Dagger\Exception\CliFallbackFailed;
use Dagger\Exception\CliReleaseUnavailable;
use GraphQL\Client;
use Psr\Log\LoggerAwareInterface;
use Psr\Log\LoggerInterface;
use Psr\Log\NullLogger;
use RuntimeException;
use Symfony\Component\Process\ExecutableFinder;
use Symfony\Component\Process\Process;
use Symfony\Component\Process\InputStream;
use Throwable;

/**
 * @deprecated
 * dagger modules will always have the environment variables set
 * so we don't need to download a CLI Client
 */
class ProcessSessionConnection extends Connection implements LoggerAwareInterface
{
    private ?Process $sessionProcess = null;
    private LoggerInterface $logger;

    public function __construct(
        private readonly string $workDir,
        private readonly bool $loadWorkspaceModules,
        private readonly CliDownloader $cliDownloader
    ) {
        $this->logger = new NullLogger();
    }

    public function connect(): Client
    {
        if (isset($this->client)) {
            return $this->client;
        }

        [$cliBinPath, $downloadError] = $this->getCliPath();

        try {
            return $this->startCliSession($cliBinPath);
        } catch (Throwable $sessionError) {
            if (null !== $downloadError) {
                throw new CliFallbackFailed(
                    $downloadError,
                    $sessionError,
                    sprintf('failed to use CLI from PATH "%s"', $cliBinPath),
                );
            }

            throw $sessionError;
        }
    }

    protected function startCliSession(string $cliBinPath): Client
    {
        $sdkVersion = Provisioning::getSdkVersion();

        $sessionInformation = null;
        $process = new Process([
            $cliBinPath,
            'session',
            '--workdir',
            $this->workDir,
            '--label',
            'dagger.io/sdk.name:php',
            '--label',
            "dagger.io/sdk.version:{$sdkVersion}",
        ]);
        if ($this->loadWorkspaceModules) {
            $process = new Process([
                $cliBinPath,
                'session',
                '--workdir',
                $this->workDir,
                '--label',
                'dagger.io/sdk.name:php',
                '--label',
                "dagger.io/sdk.version:{$sdkVersion}",
                '--load-workspace-modules',
            ]);
        }

        $process->setTimeout(null);
        $process->setInput(new InputStream());
        $process->start(function ($type, $output) {
            if (Process::ERR === $type) {
                $this->logger->error($output);
            } else {
                $this->logger->info($output);
            }
        });
        $this->logger->info('Starting Dagger session');
        $process->waitUntil(function ($type, $output) use (&$sessionInformation) {
            $this->logger->debug($output);
            if (Process::OUT === $type) {
                if (str_contains((string) $output, 'session_token')) {
                    // @TODO Rewrite when PHP 8.3 json_validate is available
                    $lines = explode("\n", (string) $output);
                    $validLines = array_filter($lines, function ($line) {
                        $this->logger->debug($line);
                        json_decode(trim($line));

                        return JSON_ERROR_NONE === json_last_error();
                    });
                    $sessionInformation = json_decode(array_shift($validLines));
                    $this->logger->info("Started Dagger session on port {$sessionInformation->port}");

                    return true;
                }
            }

            return false;
        });

        if (null === $sessionInformation) {
            throw new RuntimeException('Cannot fetch informations from process session');
        }

        $port = $sessionInformation->port;
        $token = $sessionInformation->session_token;

        $this->client = new Client('http://127.0.0.1:' . $port . '/query', [
            'Authorization' => 'Basic ' . base64_encode($token . ':'),
        ]);

        $this->sessionProcess = $process;

        return $this->client;
    }

    /**
     * @internal
     */
    public function getSessionProcess(): ?Process
    {
        return $this->sessionProcess;
    }

    public function setLogger(LoggerInterface $logger): void
    {
        $this->logger = $logger;
    }

    public function close(): void
    {
        if (isset($this->sessionProcess)) {
            $this->sessionProcess->stop(signal: 15); // SIGTERM
        }
        $this->client = null;
    }

    public function __destruct()
    {
        $this->close();
    }

    /** @return array{string, CliReleaseUnavailable|null} */
    private function getCliPath(): array
    {
        $cliBinPath = getenv('_EXPERIMENTAL_DAGGER_CLI_BIN');
        if (false !== $cliBinPath) {
            return [$cliBinPath, null];
        }

        try {
            return [$this->cliDownloader->download(), null];
        } catch (CliReleaseUnavailable $downloadError) {
            return [$this->fallbackToLocalCli($downloadError), $downloadError];
        }
    }

    /**
     * @internal
     */
    public function fallbackToLocalCli(Throwable $downloadError): string
    {
        if (!$downloadError instanceof CliReleaseUnavailable) {
            throw $downloadError;
        }

        $binPath = (new ExecutableFinder())->find('dagger');
        if (null === $binPath) {
            throw new CliFallbackFailed(
                $downloadError,
                new RuntimeException('dagger executable was not found'),
                'dagger CLI not found in PATH',
            );
        }
        if (!self::isAbsolutePath($binPath)) {
            throw new CliFallbackFailed(
                $downloadError,
                new RuntimeException(sprintf(
                    'cannot run dagger executable found relative to the current directory: %s',
                    $binPath,
                )),
                'dagger CLI not found in PATH',
            );
        }

        $warning = sprintf(
            'CLI version %s is unavailable; using %s from PATH (version compatibility is not guaranteed).',
            Provisioning::getCliVersion(),
            $binPath,
        );
        if ($this->logger instanceof NullLogger) {
            $output = $this->warningOutput();
            if (is_resource($output)) {
                fwrite($output, $warning . PHP_EOL);
            }
        } else {
            $this->logger->warning($warning);
        }

        return $binPath;
    }

    /** @return resource|false */
    protected function warningOutput()
    {
        // STDERR is only defined under the CLI SAPI; web SAPIs (fpm, mod_php)
        // need an explicit stream.
        return defined('STDERR') ? STDERR : fopen('php://stderr', 'w');
    }

    private static function isAbsolutePath(string $path): bool
    {
        return str_starts_with($path, '/')
            || str_starts_with($path, '\\')
            || 1 === preg_match('#^[A-Za-z]:[/\\\\]#', $path);
    }
}
