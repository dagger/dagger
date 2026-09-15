<?php

namespace Dagger\Codegen;

use Dagger\Codegen\Introspection\IntrospectionSchema;
use GraphQL\Client;

class SchemaGenerator
{
    private array $schemaArray;
    private IntrospectionSchema $schema;

    public function __construct(private readonly Client $client)
    {
        $this->update();
    }

    public function getSchema(): IntrospectionSchema
    {
        return $this->schema;
    }

    public function getRawData(): array
    {
        return $this->schemaArray;
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
        $this->schema = IntrospectionSchema::fromArray($this->schemaArray);
    }
}
