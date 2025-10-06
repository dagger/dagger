<?php

namespace Dagger\Connection;

use Dagger\Connection;
use GraphQL\Client;

class EnvSessionConnection extends Connection
{
    public function connect(): Client
    {
        if (isset($this->client)) {
            return $this->client;
        }

        $port = (int) getenv('DAGGER_SESSION_PORT');
        $token = getenv('DAGGER_SESSION_TOKEN');

        $this->client = self::createGraphQlClient(
            $port,
            $token
        );

        return $this->client;
    }

    public function close(): void
    {
        // noop
    }
}
