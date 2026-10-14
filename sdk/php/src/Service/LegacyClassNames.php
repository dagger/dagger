<?php

declare(strict_types=1);

namespace Dagger\Service;

/**
 * Finds a generated class under an older spelling of its name.
 *
 * Generated classes follow PSR-1's convention for acronyms (Llm, SdkConfig,
 * HttpState) where they used to keep the schema's spelling (LLM, SDKConfig,
 * HTTPState). Only letter case changed, and PHP class names are
 * case-insensitive, so `\Dagger\LLM` still names the class declared as
 * `\Dagger\Llm`. But PSR-4 autoloading looks for `LLM.php`, which a
 * case-sensitive filesystem doesn't find: this autoloader, registered after
 * Composer's, finds the generated file ignoring case.
 *
 * Loading a class under an older spelling is deprecated.
 */
final class LegacyClassNames
{
    private const PREFIX = 'Dagger\\';

    /** @var ?array<string, string> generated files by lowercase class name */
    private static ?array $files = null;

    public static function register(): void
    {
        spl_autoload_register(self::autoload(...));
    }

    public static function autoload(string $className): void
    {
        $file = self::fileFor($className);
        if ($file === null) {
            return;
        }

        $actual = self::PREFIX . basename($file, '.php');
        if ($actual !== ltrim($className, '\\')) {
            @trigger_error(
                sprintf('Class "%s" is deprecated, use "%s" instead.', $className, $actual),
                E_USER_DEPRECATED,
            );
        }

        require_once $file;
    }

    /**
     * The generated file declaring a class, matching its name ignoring case,
     * or null if the class isn't a generated one.
     */
    public static function fileFor(string $className, ?string $directory = null): ?string
    {
        $className = ltrim($className, '\\');
        if (!str_starts_with($className, self::PREFIX)) {
            return null;
        }
        $shortName = substr($className, strlen(self::PREFIX));
        if ($shortName === '' || str_contains($shortName, '\\')) {
            return null;
        }

        return self::files($directory)[strtolower($shortName)] ?? null;
    }

    /**
     * @return array<string, string>
     */
    private static function files(?string $directory): array
    {
        if ($directory !== null) {
            return self::scan($directory);
        }

        return self::$files ??= self::scan(dirname(__DIR__, 2) . DIRECTORY_SEPARATOR . 'generated');
    }

    /**
     * @return array<string, string>
     */
    private static function scan(string $directory): array
    {
        $files = [];
        foreach (glob($directory . DIRECTORY_SEPARATOR . '*.php') ?: [] as $file) {
            $files[strtolower(basename($file, '.php'))] = $file;
        }

        return $files;
    }
}
