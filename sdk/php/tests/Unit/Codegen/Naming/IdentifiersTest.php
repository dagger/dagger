<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Codegen\Naming;

use Dagger\Codegen\Naming\Casing;
use Dagger\Codegen\Naming\IdentifierWord;
use Dagger\Codegen\Naming\Identifiers;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(Identifiers::class)]
#[CoversClass(IdentifierWord::class)]
class IdentifiersTest extends TestCase
{
    /**
     * The engine's shared test vectors, relative to the Dagger repository.
     * DAGGER_NAMING_VECTORS overrides the path.
     */
    private const VECTORS = 'engine/naming/testdata/vectors.json';

    /** @var array<string, array{0: Casing, 1: bool}> */
    private const FORMATS = [
        'PASCAL' => [Casing::PASCAL, false],
        'PASCAL_CAPITALIZED' => [Casing::PASCAL, true],
        'CAMEL' => [Casing::CAMEL, false],
        'CAMEL_CAPITALIZED' => [Casing::CAMEL, true],
        'SNAKE' => [Casing::SNAKE, false],
        'SCREAMING_SNAKE' => [Casing::SCREAMING_SNAKE, false],
        'KEBAB' => [Casing::KEBAB, false],
        'FLAT' => [Casing::FLAT, false],
    ];

    #[Test]
    #[DataProvider('vectors')]
    public function itFormatsSharedVectors(string $input, array $words, string $format, string $expected): void
    {
        if (!isset(self::FORMATS[$format])) {
            self::fail($expected);
        }
        [$casing, $capitalized] = self::FORMATS[$format];
        $parsed = array_map(static fn(array $word) => IdentifierWord::fromArray($word), $words);

        self::assertNotContains(null, $parsed, "{$input} has a word of an unknown kind");
        self::assertSame($expected, Identifiers::formatWords($parsed, $casing, $capitalized));
    }

    /**
     * @return iterable<string, array{string, array, string, string}>
     */
    public static function vectors(): iterable
    {
        $path = getenv('DAGGER_NAMING_VECTORS') ?: dirname(__DIR__, 6) . '/' . self::VECTORS;
        if (!is_file($path)) {
            yield 'vectors missing' => ['', [], 'MISSING', "naming vectors not found at {$path}"];
            return;
        }

        $vectors = json_decode((string)file_get_contents($path), true, flags: JSON_THROW_ON_ERROR);
        foreach ($vectors as $i => $vector) {
            foreach ($vector['formats'] as $format => $expected) {
                if (!isset(self::FORMATS[$format])) {
                    continue;
                }
                yield "#{$i} {$vector['input']} {$format}" => [$vector['input'], $vector['words'], $format, $expected];
            }
        }
    }

    #[Test]
    public function itReturnsNullWithoutWords(): void
    {
        $identifiers = Identifiers::fromArray([
            'httpClient' => [
                ['kind' => 'ACRONYM', 'text' => 'HTTP', 'suffix' => '', 'capitalized' => 'Http'],
                ['kind' => 'WORD', 'text' => 'client', 'suffix' => '', 'capitalized' => 'Client'],
            ],
            '_internal' => [
                ['kind' => 'WORD', 'text' => 'internal', 'suffix' => '', 'capitalized' => 'Internal'],
            ],
            'future' => [
                ['kind' => 'SOMETHING_NEW', 'text' => 'x', 'suffix' => '', 'capitalized' => 'X'],
            ],
            'empty' => [],
        ]);

        self::assertSame('HTTPClient', $identifiers->format('httpClient', Casing::PASCAL));
        self::assertSame('HttpClient', $identifiers->format('httpClient', Casing::PASCAL, true));
        self::assertNull($identifiers->format('notInSchema', Casing::CAMEL));
        self::assertNull($identifiers->format('_internal', Casing::CAMEL));
        self::assertNull($identifiers->format('future', Casing::CAMEL));
        self::assertNull($identifiers->format('empty', Casing::CAMEL));
        self::assertNull((new Identifiers())->format('httpClient', Casing::CAMEL));
    }
}
