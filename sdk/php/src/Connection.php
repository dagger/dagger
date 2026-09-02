<?php

namespace Dagger;

use Dagger\Connection\CliDownloader;
use Dagger\Connection\EnvSessionConnection;
use Dagger\Connection\ProcessSessionConnection;
use GraphQL\Client;
use InvalidArgumentException;

abstract class Connection
{
    protected ?Client $client;

    public static function get(string $workingDir = '', bool $loadWorkspaceModules = false): Connection
    {
        $connection = static::newEnvSession();

        if (null !== $connection && !empty($workingDir)) {
            throw new InvalidArgumentException(
                'cannot configure workdir for existing session' .
                ' (please use --workdir or host.directory with absolute paths instead)'
            );
        }

        if (null === $connection) {
            $connection = static::newProcessSession($workingDir, $loadWorkspaceModules, new CliDownloader());
        }

        return $connection;
    }

    public static function newEnvSession(): ?EnvSessionConnection
    {
        $nesting = getenv('DAGGER_NESTING');
        $port = getenv('DAGGER_SESSION_PORT');
        $token = getenv('DAGGER_SESSION_TOKEN');

        if (
            false !== $nesting
            && '' !== $nesting
            && !in_array($nesting, ['NESTED_CLIENT', 'INDEPENDENT_SESSIONS'], true)
        ) {
            throw new InvalidArgumentException("unknown DAGGER_NESTING value {$nesting}");
        }
        if (in_array($nesting, ['NESTED_CLIENT', 'INDEPENDENT_SESSIONS'], true) && false === $port) {
            throw new InvalidArgumentException("DAGGER_NESTING={$nesting} requires DAGGER_SESSION_PORT");
        }
        if ('NESTED_CLIENT' === $nesting && false === $token) {
            throw new InvalidArgumentException('DAGGER_SESSION_TOKEN must be set when using DAGGER_SESSION_PORT');
        }
        if ('INDEPENDENT_SESSIONS' === $nesting) {
            if (!ctype_digit((string) $port) || (int) $port < 1) {
                throw new InvalidArgumentException("invalid DAGGER_SESSION_PORT value {$port}");
            }

            return null;
        }

        if (false === $port || false === $token) {
            return null;
        }

        return new EnvSessionConnection();
    }

    /**
     * @deprecated
     * dagger modules will always have the environment variables set
     * so we don't need to download a CLI Client
     */
    public static function newProcessSession(
        string $workDir,
        bool $loadWorkspaceModules,
        CliDownloader $cliDownloader
    ): ProcessSessionConnection {
        return new ProcessSessionConnection($workDir, $loadWorkspaceModules, $cliDownloader);
    }

    protected static function createGraphQlClient(int $port, string $token): Client
    {
        $encodedToken = base64_encode("{$token}:");
        $headers = [
            'Authorization' => "Basic {$encodedToken}",
        ];

        $traceparent = getenv('TRACEPARENT');
        if ($traceparent) {
            $headers = array_merge($headers, ['Traceparent' => $traceparent]);
        }

        return new Client("http://127.0.0.1:{$port}/query", $headers);
    }

    abstract public function connect(): Client;

    abstract public function close(): void;
}
