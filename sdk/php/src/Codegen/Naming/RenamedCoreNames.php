<?php

declare(strict_types=1);

namespace Dagger\Codegen\Naming;

/**
 * Core schema names renamed for the engine's naming rules (see
 * hack/designs/identifier-casing.md, "Core schema", in the Dagger
 * repository), with their old schema names. The engine still accepts the old
 * names as hidden aliases, but the schema doesn't show them, so the PHP names
 * derived from them are kept from here.
 *
 * Only the names whose PHP names changed are listed: input object fields,
 * whose properties are named after the schema, and enum values, whose old
 * names stay as deprecated constants. Methods and parameters are formatted
 * CAMEL / CAPITALIZED, which spells the old and new names alike.
 */
final class RenamedCoreNames
{
    /** @var array<string, string> old schema names by "Type.name" */
    private const NAMES = [
        'FunctionCachePolicy.DEFAULT' => 'Default',
        'FunctionCachePolicy.NEVER' => 'Never',
        'FunctionCachePolicy.PER_SESSION' => 'PerSession',
        'LLMContentBlockInput.callID' => 'callId',
    ];

    /**
     * The schema name an input object field or enum value of $typeName had
     * before it was renamed: $name itself, unless it's a renamed core name.
     */
    public static function legacyName(string $typeName, string $name): string
    {
        return self::NAMES["{$typeName}.{$name}"] ?? $name;
    }
}
