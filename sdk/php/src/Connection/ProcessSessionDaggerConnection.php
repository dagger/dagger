<?php

namespace DaggerIo\Connection;

use Composer\InstalledVersions;
use DaggerIo\DaggerConnection;
use Exception;
use GraphQL\Client;
use Psr\Log\LoggerAwareInterface;
use Psr\Log\LoggerInterface;
use Psr\Log\NullLogger;
use Symfony\Component\Process\Process;

class ProcessSessionDaggerConnection extends DaggerConnection implements LoggerAwareInterface
{
    private ?Process $sessionProcess;
    private ?Client $client;
    private LoggerInterface $logger;

    public function __construct(
        private readonly string $workDir,
        private readonly CliDownloader $cliDownloader
    ) {
        $this->logger = new NullLogger();
    }

    public function getGraphQlClient(): Client
    {
        if (isset($this->client)) {
            return $this->client;
        }

        $cliBinPath = $this->getCliPath();
        $sdkVersion = $this->getSdkVersion();

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
        ], env: [
                'DAGGER_SESSION_TOKEN' => false,
                'DAGGER_SESSION_PORT' => false,
        ]
        );

        $process->setTimeout(null);
        $process->setPty(true);
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
            throw new \RuntimeException('Cannot fetch informations from process session');
        }

        $port = $sessionInformation->port;
        $token = $sessionInformation->session_token;

        $this->client = new Client('http://127.0.0.1:'.$port.'/query', [
            'Authorization' => 'Basic '.base64_encode($token.':'),
        ]);

        $this->sessionProcess = $process;

        return $this->client;
    }

    public function getVersion(): string
    {
        $cliBinPath = $this->getCliPath();
        $process = new Process([$cliBinPath, 'version']);
        $process->mustRun();

        return $process->getOutput();
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
            $this->sessionProcess->stop();
        }
        $this->client = null;
    }

    public function __destruct()
    {
        $this->close();
    }

    private function getCliPath(): string
    {
        $cliBinPath = getenv('_EXPERIMENTAL_DAGGER_CLI_BIN');
        if (false === $cliBinPath) {
            $cliBinPath = $this->cliDownloader->download();
        }

        return $cliBinPath;
    }

    private function getSdkVersion(): string
    {
        $projectDir = dirname(__DIR__, 2);

        try {
            $version = InstalledVersions::getVersion('dagger.io/dagger-php-sdk') ?? 'dev';
        } catch (Exception) {
            $version = 'dev';
        }

        return $version;
    }
}
