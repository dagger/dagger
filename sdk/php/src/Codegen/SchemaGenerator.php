<?php

namespace Dagger\Codegen;

use Dagger\Codegen\Introspection\IntrospectionSchema;
use Dagger\Codegen\Introspection\NewCodegenVisitor;
use Dagger\Codegen\Naming\FormattedNames;
use GraphQL\Client;
use RuntimeException;

class SchemaGenerator
{
    /** Bounds the size of the names sent per formatIdentifiers request. */
    private const FORMAT_BATCH_BYTES = 256 << 10;

    private const FORMAT_IDENTIFIERS_QUERY = <<<'GRAPHQL'
        query FormatIdentifiers($names: [String!]!, $casing: Casing!, $acronyms: AcronymStyle) {
          formatIdentifiers(names: $names, casing: $casing, acronyms: $acronyms)
        }
        GRAPHQL;

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
        $this->schema->names = $this->formatNames();
    }

    /**
     * Has the engine format the schema's names, with Query.formatIdentifiers,
     * in each format the generator uses. The engine parses them with the
     * dictionary for the client's version.
     *
     * Returns null when the schema has no Query.formatIdentifiers (engine
     * views before v1.0.0), so codegen keeps its legacy conversion.
     */
    private function formatNames(): ?FormattedNames
    {
        if (!$this->schema->hasFormatIdentifiers()) {
            return null;
        }

        $names = $this->schema->names();
        $formatted = [];
        foreach (NewCodegenVisitor::NAME_FORMATS as [$casing, $acronyms]) {
            $key = FormattedNames::key($casing, $acronyms);
            $formatted[$key] = [];
            foreach ($this->batches($names) as $batch) {
                // The client JSON-encodes any variables, despite documenting
                // them as array<string, string>.
                // @phpstan-ignore argument.type
                $result = $this->client->runRawQuery(self::FORMAT_IDENTIFIERS_QUERY, true, [
                    'names' => $batch,
                    'casing' => $casing->value,
                    'acronyms' => $acronyms->value,
                ])->getData();

                $values = $result['formatIdentifiers'] ?? null;
                if (!is_array($values) || count($values) !== count($batch)) {
                    throw new RuntimeException(sprintf(
                        'format names as %s: sent %d names, got %d back',
                        $key,
                        count($batch),
                        is_array($values) ? count($values) : 0,
                    ));
                }
                foreach ($batch as $i => $name) {
                    $formatted[$key][$name] = (string)$values[$i];
                }
            }
        }

        return new FormattedNames($formatted);
    }

    /**
     * Splits names into batches of at most FORMAT_BATCH_BYTES (but at least
     * one name each). The core schema's names fit in one.
     *
     * @param list<string> $names
     * @return list<list<string>>
     */
    private function batches(array $names): array
    {
        $batches = [];
        $batch = [];
        $size = 0;
        foreach ($names as $name) {
            if ($batch !== [] && $size + strlen($name) > self::FORMAT_BATCH_BYTES) {
                $batches[] = $batch;
                $batch = [];
                $size = 0;
            }
            $batch[] = $name;
            $size += strlen($name);
        }
        if ($batch !== []) {
            $batches[] = $batch;
        }

        return $batches;
    }
}
