<?php

declare(strict_types=1);

namespace Dagger\Codegen\Naming;

/**
 * The schema's names as the engine formatted them with
 * Query.formatIdentifiers, by name format: the shape of the sidecar file
 * `codegen introspect --names-out` writes,
 *
 *     {"CAMEL:CAPITALIZED": {"withGPU": "withGpu", ...}, ...}
 *
 * Codegen doesn't format names itself, so the formatting rules and the
 * dictionary live only in the engine (see hack/designs/identifier-casing.md,
 * "Formatting names in codegen", in the Dagger repository). Every lookup
 * returns null for a format or name the engine didn't format, so codegen
 * keeps its legacy conversion for it.
 */
final readonly class FormattedNames
{
    /**
     * @param array<string, array<string, string>> $names formatted names by
     *   format ("CAMEL:CAPITALIZED"), then by schema name
     */
    public function __construct(private array $names = [])
    {
    }

    /**
     * Reads the sidecar file's decoded JSON. Values that aren't strings are
     * left out.
     */
    public static function fromArray(array $data): self
    {
        $names = [];
        foreach ($data as $format => $formatted) {
            if (!is_array($formatted)) {
                continue;
            }
            foreach ($formatted as $name => $value) {
                if (is_string($value) && $value !== '') {
                    $names[(string)$format][(string)$name] = $value;
                }
            }
        }

        return new self($names);
    }

    /**
     * The text form of a name format, as the sidecar file's keys have it:
     * "CASING:ACRONYMS".
     */
    public static function key(Casing $casing, AcronymStyle $acronyms): string
    {
        return $casing->value . ':' . $acronyms->value;
    }

    /**
     * A schema name as the engine formatted it, or null when it wasn't
     * formatted in that casing and acronym style.
     *
     * Names with a leading underscore (internal names) are never formatted:
     * the formatted name wouldn't carry the underscore.
     */
    public function format(
        string $name,
        Casing $casing,
        AcronymStyle $acronyms = AcronymStyle::UPPERCASE,
    ): ?string {
        if (str_starts_with($name, '_')) {
            return null;
        }

        return $this->names[self::key($casing, $acronyms)][$name] ?? null;
    }

    /**
     * @return array<string, array<string, string>>
     */
    public function toArray(): array
    {
        return $this->names;
    }
}
