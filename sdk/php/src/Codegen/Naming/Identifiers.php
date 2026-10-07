<?php

declare(strict_types=1);

namespace Dagger\Codegen\Naming;

/**
 * The words the engine parsed each schema name into: the schema JSON's
 * "__identifiers" map, present for engine views v1.0.0 and above.
 *
 * Formatting needs no dictionary (see hack/designs/identifier-casing.md,
 * "Schema JSON words", in the Dagger repository). Every lookup returns null
 * when the schema has no words for a name, so codegen keeps its own
 * conversion for older schemas.
 */
final readonly class Identifiers
{
    /**
     * @param array<string, IdentifierWord[]> $words
     */
    public function __construct(private array $words = [])
    {
    }

    /**
     * Builds the map from the schema JSON's "__identifiers" value. Names with
     * no words, or with a word of a kind this SDK doesn't know, are left out.
     */
    public static function fromArray(array $data): self
    {
        $words = [];
        foreach ($data as $name => $wordsData) {
            if (!is_array($wordsData) || $wordsData === []) {
                continue;
            }
            $nameWords = [];
            foreach ($wordsData as $wordData) {
                $word = is_array($wordData) ? IdentifierWord::fromArray($wordData) : null;
                if ($word === null) {
                    continue 2;
                }
                $nameWords[] = $word;
            }
            $words[(string)$name] = $nameWords;
        }

        return new self($words);
    }

    /**
     * The words of a schema name, or null when the schema has none for it.
     *
     * Names with a leading underscore (internal names) never have words: the
     * words wouldn't carry the underscore.
     *
     * @return IdentifierWord[]|null
     */
    public function words(string $name): ?array
    {
        if (str_starts_with($name, '_')) {
            return null;
        }

        return $this->words[$name] ?? null;
    }

    /**
     * Formats a schema name in a casing, or returns null when the schema has
     * no words for it.
     *
     * @param bool $capitalizedAcronyms CAPITALIZED acronym style (Http)
     *   rather than UPPERCASE (HTTP)
     */
    public function format(string $name, Casing $casing, bool $capitalizedAcronyms = false): ?string
    {
        $words = $this->words($name);
        if ($words === null) {
            return null;
        }

        return self::formatWords($words, $casing, $capitalizedAcronyms);
    }

    /**
     * @param IdentifierWord[] $words
     */
    public static function formatWords(array $words, Casing $casing, bool $capitalizedAcronyms = false): string
    {
        $parts = [];
        foreach (array_values($words) as $i => $word) {
            $parts[] = match ($casing) {
                Casing::PASCAL => $word->capitalized($capitalizedAcronyms),
                Casing::CAMEL => $i === 0 ? $word->lower() : $word->capitalized($capitalizedAcronyms),
                Casing::SCREAMING_SNAKE => $word->upper(),
                Casing::SNAKE, Casing::KEBAB, Casing::FLAT => $word->lower(),
            };
        }

        return implode(match ($casing) {
            Casing::SNAKE, Casing::SCREAMING_SNAKE => '_',
            Casing::KEBAB => '-',
            default => '',
        }, $parts);
    }
}
