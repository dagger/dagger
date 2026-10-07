<?php

namespace Dagger\Codegen;

use Dagger\Codegen\Introspection\IntrospectionSchema;
use GraphQL\Client;
use RuntimeException;

class SchemaGenerator
{
    /** Bounds the number of names parsed per identifier request. */
    private const IDENTIFIER_BATCH_SIZE = 500;

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

        $identifiers = $this->fetchIdentifiers();
        if ($identifiers !== null) {
            $this->schemaArray['__identifiers'] = $identifiers;
            $this->schema = IntrospectionSchema::fromArray($this->schemaArray);
        }
    }

    /**
     * Fetches the words of the schema's names from the engine's
     * Query.identifier API, in the shape of the schema JSON's "__identifiers"
     * map, since the introspection query can't carry them. The engine parses
     * them with the dictionary for the client's version.
     *
     * Returns null when the schema has no Query.identifier (engine views
     * before v1.0.0), so codegen keeps its own conversion.
     *
     * @return array<string, list<array{kind: string, text: string, suffix: string, capitalized: string}>>|null
     */
    private function fetchIdentifiers(): ?array
    {
        $query = $this->schema->getType('Query');
        if ($query === null || !$query->hasField('identifier')) {
            return null;
        }

        $identifiers = [];
        foreach (array_chunk($this->identifierNames(), self::IDENTIFIER_BATCH_SIZE) as $batch) {
            $fields = [];
            foreach ($batch as $i => $name) {
                $fields[] = sprintf(
                    'i%d: identifier(name: %s) { words { kind text suffix term { spelling capitalized } } }',
                    $i,
                    json_encode($name, JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR),
                );
            }
            $data = $this->client
                ->runRawQuery('query Identifiers { ' . implode(' ', $fields) . ' }', true)
                ->getData();

            foreach ($batch as $i => $name) {
                $result = $data["i{$i}"] ?? null;
                if (!is_array($result)) {
                    throw new RuntimeException("identifier query: no result for \"{$name}\"");
                }
                $words = [];
                foreach ($result['words'] ?? [] as $word) {
                    $words[] = [
                        'kind' => $word['kind'],
                        'text' => $word['text'],
                        'suffix' => $word['suffix'],
                        // The word's CAPITALIZED form: the dictionary entry's,
                        // else the first letter capitalized and the rest
                        // lowercase.
                        'capitalized' => $word['term']['capitalized'] ?? ucfirst(strtolower($word['text'])),
                    ];
                }
                $identifiers[$name] = $words;
            }
        }

        return $identifiers;
    }

    /**
     * The distinct type, field, argument, input field and enum value names
     * in the schema, sorted, leaving out introspection names ("__" prefix)
     * and names with no letters or digits.
     *
     * @return list<string>
     */
    private function identifierNames(): array
    {
        $names = [];
        $add = static function (string $name) use (&$names): void {
            if (str_starts_with($name, '__') || preg_match('/[A-Za-z0-9]/', $name) !== 1) {
                return;
            }
            $names[$name] = true;
        };

        foreach ($this->schema->types as $type) {
            if (str_starts_with($type->name, '__')) {
                continue;
            }
            $add($type->name);
            foreach ($type->fields as $field) {
                $add($field->name);
                foreach ($field->args as $arg) {
                    $add($arg->name);
                }
            }
            foreach ($type->inputFields as $inputField) {
                $add($inputField->name);
            }
            foreach ($type->enumValues as $enumValue) {
                $add($enumValue->name);
            }
        }

        $names = array_map('strval', array_keys($names));
        sort($names, SORT_STRING);

        return $names;
    }
}
