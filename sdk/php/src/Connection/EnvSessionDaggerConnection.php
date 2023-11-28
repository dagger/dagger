<?php

namespace DaggerIo\Connection;

use DaggerIo\DaggerConnection;
use GraphQL\Client;

class EnvSessionDaggerConnection extends DaggerConnection
{
    private ?Client $client;

    public function __construct(
        private readonly string $daggerSessionPort,
        private readonly string $daggerSessionToken
    ) {
    }

    public function getGraphQlClient(): Client
    {
        if (isset($this->client)) {
            return $this->client;
        }

        $this->client = new Client('127.0.0.1:'.$this->daggerSessionPort.'/query', [
            'Authorization' => 'Basic '.base64_encode($this->daggerSessionToken.':'),
        ]);

        return $this->client;
    }

    public function close(): void
    {
        // noop
    }
}
