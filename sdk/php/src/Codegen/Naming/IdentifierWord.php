<?php

declare(strict_types=1);

namespace Dagger\Codegen\Naming;

/**
 * One word of a schema name, as the engine parsed it, from the schema JSON's
 * "__identifiers" map.
 */
final readonly class IdentifierWord
{
    public const KIND_WORD = 'WORD';
    public const KIND_ACRONYM = 'ACRONYM';
    public const KIND_TERM = 'TERM';

    /**
     * @param string $kind WORD, ACRONYM or TERM
     * @param string $text the standard spelling: client, HTTP, GitHub
     * @param string $suffix a plural "s" and/or trailing digits: SHA+"s", OAuth+"2"
     * @param string $capitalized the CAPITALIZED form, without the suffix: Client, Http, GitHub
     */
    public function __construct(
        public string $kind,
        public string $text,
        public string $suffix,
        public string $capitalized,
    ) {
    }

    /**
     * Returns null for a word of a kind this SDK doesn't know (from a newer
     * engine), so the caller can fall back to its own conversion.
     *
     * @param array{kind?: string, text?: string, suffix?: string, capitalized?: string} $data
     */
    public static function fromArray(array $data): ?self
    {
        $kind = $data['kind'] ?? '';
        if (!in_array($kind, [self::KIND_WORD, self::KIND_ACRONYM, self::KIND_TERM], true)) {
            return null;
        }

        return new self(
            $kind,
            $data['text'] ?? '',
            $data['suffix'] ?? '',
            $data['capitalized'] ?? '',
        );
    }

    /**
     * The word in lowercase, with its suffix.
     */
    public function lower(): string
    {
        return strtolower($this->text . $this->suffix);
    }

    /**
     * The word in uppercase, with its suffix.
     */
    public function upper(): string
    {
        return strtoupper($this->text . $this->suffix);
    }

    /**
     * The word's form where it starts with a capital, with its suffix.
     *
     * With UPPERCASE acronyms (the default), an ACRONYM or TERM keeps its
     * spelling with the first letter uppercased (HTTP, GitHub); a WORD, and
     * every word with CAPITALIZED acronyms, uses its capitalized form (Http).
     */
    public function capitalized(bool $capitalizedAcronyms = false): string
    {
        if ($capitalizedAcronyms || $this->kind === self::KIND_WORD) {
            return $this->capitalized . $this->suffix;
        }

        return ucfirst($this->text) . $this->suffix;
    }
}
