<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Codegen;

use Dagger\Codegen\Naming\AcronymStyle;
use Dagger\Codegen\Naming\Casing;
use Dagger\Codegen\SchemaGenerator;
use GraphQL\Client;
use GraphQL\Results;
use GuzzleHttp\Psr7\Response;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(SchemaGenerator::class)]
class SchemaGeneratorTest extends TestCase
{
    #[Test]
    public function itFormatsNamesThroughTheEngine(): void
    {
        $requests = [];
        $generator = new SchemaGenerator($this->client(true, $requests));
        $names = $generator->getSchema()->names;

        self::assertNotNull($names);
        self::assertSame('withGpu', $names->format('withGPU', Casing::CAMEL, AcronymStyle::CAPITALIZED));
        self::assertSame('callId', $names->format('callID', Casing::CAMEL, AcronymStyle::CAPITALIZED));
        self::assertSame('PER_SESSION', $names->format('PerSession', Casing::SCREAMING_SNAKE));
        self::assertNull($names->format('__typename', Casing::CAMEL, AcronymStyle::CAPITALIZED));

        // one request per name format, with the names as a variable; no
        // introspection names, no names without letters or digits
        $sorted = [
            'Mode', 'PerSession', 'Query', 'String', 'callID', 'formatIdentifiers', 'withGPU',
        ];
        self::assertSame([
            ['names' => $sorted, 'casing' => 'CAMEL', 'acronyms' => 'CAPITALIZED'],
            ['names' => $sorted, 'casing' => 'SCREAMING_SNAKE', 'acronyms' => 'UPPERCASE'],
        ], $requests);

        // the schema JSON stays plain introspection
        self::assertSame(['__schema'], array_keys($generator->getRawData()));
    }

    #[Test]
    public function itKeepsLegacyNamesWithoutFormatIdentifiers(): void
    {
        $requests = [];
        $generator = new SchemaGenerator($this->client(false, $requests));

        self::assertNull($generator->getSchema()->names);
        self::assertSame([], $requests);
    }

    /**
     * A GraphQL client serving a schema, with or without
     * Query.formatIdentifiers, that formats names from a fixed map and
     * records the variables of each formatIdentifiers request.
     *
     * @param list<array<string, mixed>> $requests
     */
    private function client(bool $formatIdentifiers, array &$requests): Client
    {
        $string = ['kind' => 'SCALAR', 'name' => 'String'];
        $queryFields = [[
            'name' => 'withGPU',
            'type' => $string,
            'args' => [['name' => 'callID', 'type' => $string], ['name' => '_', 'type' => $string]],
        ]];
        if ($formatIdentifiers) {
            $queryFields[] = ['name' => 'formatIdentifiers', 'type' => $string, 'args' => []];
        }
        $schema = ['__schema' => ['types' => [
            ['kind' => 'OBJECT', 'name' => 'Query', 'fields' => $queryFields],
            ['kind' => 'SCALAR', 'name' => 'String'],
            ['kind' => 'ENUM', 'name' => 'Mode', 'enumValues' => [['name' => 'PerSession']]],
            ['kind' => 'OBJECT', 'name' => '__Type', 'fields' => [['name' => 'kind', 'type' => $string]]],
        ]]];
        $formatted = [
            'CAMEL:CAPITALIZED' => ['withGPU' => 'withGpu', 'callID' => 'callId'],
            'SCREAMING_SNAKE:UPPERCASE' => ['PerSession' => 'PER_SESSION'],
        ];

        $client = $this->createStub(Client::class);
        $client->method('runRawQuery')->willReturnCallback(
            static function (string $query, bool $asArray = false, array $variables = []) use (
                $schema,
                $formatted,
                &$requests,
            ): Results {
                if (!str_contains($query, 'formatIdentifiers(')) {
                    $data = $schema;
                } else {
                    $requests[] = $variables;
                    $format = $formatted[$variables['casing'] . ':' . $variables['acronyms']] ?? [];
                    $data = ['formatIdentifiers' => array_map(
                        static fn(string $name) => $format[$name] ?? $name,
                        $variables['names'],
                    )];
                }
                return new Results(new Response(200, [], json_encode(['data' => $data])), $asArray);
            },
        );

        return $client;
    }
}
