<?php

namespace DaggerIo\Connection;

use DaggerIo\DaggerConnection;
use GraphQL\Client;
use Psr\Log\LoggerAwareInterface;
use Psr\Log\LoggerInterface;
use Psr\Log\NullLogger;
use Symfony\Component\Process\Process;

class ProcessSessionDaggerConnection extends DaggerConnection implements LoggerAwareInterface
{
    protected const BINARY = 'dagger';
    private Process $sessionProcess;
    private ?Client $client;
    private LoggerInterface $logger;

    public function __construct(
        private readonly string $workDir = '.',
    ) {
        $this->logger = new NullLogger();
    }

    public function getGraphQlClient(): Client
    {
        if (isset($this->client)) {
            return $this->client;
        }

        $sessionInformation = null;
        $process = new Process([
            self::BINARY,
            'session',
            '--workdir',
            $this->workDir,
        ]);
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

        $port = $sessionInformation->port;
        $token = $sessionInformation->session_token;

        $this->client = new Client('127.0.0.1:'.$port.'/query', [
            'Authorization' => 'Basic '.base64_encode($token.':'),
        ]);

        $this->sessionProcess = $process;

        return $this->client;
    }

    public function getVersion(): string
    {
        $process = new Process([self::BINARY, 'version']);
        $process->mustRun();

        return $process->getOutput();
    }

    /**
     * @internal
     */
    public function getSessionProcess(): Process
    {
        return $this->sessionProcess;
    }

    public function setLogger(LoggerInterface $logger): void
    {
        $this->logger = $logger;
    }

    public function close(): void
    {
        $this->sessionProcess->stop();
        $this->client = null;
    }

    public function __destruct()
    {
        $this->close();
    }
}
