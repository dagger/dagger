<?php

namespace Dagger\Codegen;

use GraphQL\Client;
use GraphQL\Type\Schema;
use GraphQL\Utils\BuildClientSchema;

class SchemaGenerator
{
    private array $schemaArray;
    private Schema $schema;

    public function __construct(private readonly Client $client)
    {
        $this->update();
    }

    public function getSchema(): Schema
    {
        return $this->schema;
    }

    public function getJson(): string
    {
        return json_encode($this->schemaArray, JSON_PRETTY_PRINT);
    }

    public function update(): void
    {
        $introspectionQueryFilePath = implode(DIRECTORY_SEPARATOR, [
            __DIR__,
            'Resources',
            'introspection.graphql',
        ]);

        $introspectionQuery = file_get_contents($introspectionQueryFilePath);

        $this->schemaArray = $this->client->runRawQuery($introspectionQuery, true)->getData();
        $this->schema = BuildClientSchema::build($this->schemaArray);
    }
}
