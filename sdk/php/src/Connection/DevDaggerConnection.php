<?php

namespace DaggerIo\Connection;

use DaggerIo\DaggerConnection;
use GraphQL\Client;

class DevDaggerConnection extends DaggerConnection
{
    private ?Client $client;

    public function getGraphQlClient(): Client
    {
        if (isset($this->client)) {
            return $this->client;
        }

        $this->client = new Client('dagger-engine:8080/query', [
            'Authorization' => 'Basic '.base64_encode('dev:'),
        ]);

        return $this->client;
    }

    public function close(): void
    {
        // noop
    }
}
