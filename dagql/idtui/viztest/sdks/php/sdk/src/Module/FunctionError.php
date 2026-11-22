<?php

declare(strict_types=1);

namespace Dagger\Module;

use CompileError;
use ErrorException;
use GraphQL\Exception\QueryError;
use JsonException;
use ParseError;
use stdClass;
use Throwable;

/**
 * Reports the error a module function failed with to the engine.
 *
 * An engine that names a file for the function's error, in
 * DAGGER_FUNCTION_ERROR_PATH, reads the error there and fails the call with
 * it, values and all, and shows it: the function's logs then get only a short
 * stack trace, through the module's own frames, and nothing for an API error
 * whose origin the engine shows. Otherwise, as with an older engine or a file
 * that can't be written, the error goes last on stderr, after its full stack
 * trace, on one marker line the generated entrypoint reads.
 */
final readonly class FunctionError
{
    /**
     * Starts the last line of stderr when the function fails, for an engine
     * that names no file for its error; the generated entrypoint reads it.
     */
    public const MARKER = '::dagger-function-error::';

    /**
     * Names the file the engine gives a call's process for its function's error.
     */
    public const PATH_ENV = 'DAGGER_FUNCTION_ERROR_PATH';

    /** The largest function error file the engine reads. */
    public const FILE_LIMIT = 1 << 20;

    /** What a report over the limit keeps of each long string, before it drops the values. */
    public const SHORTENED_LENGTH = 64 * 1024;

    /**
     * The engine hands the entrypoint the last 100 KiB of a failed exec's
     * stderr, and JSON encodes a byte in at most six.
     */
    public const MARKER_LIMIT = 16 * 1024;

    /** An origin an error's message names: the span of the call the engine reports it from. */
    public const ORIGIN = '/\s*\[traceparent:[0-9a-f]{32}-[0-9a-f]{16}\]/';

    /** The errors that end a PHP script without a Throwable to catch. */
    public const FATAL_ERRORS = E_ERROR | E_PARSE | E_CORE_ERROR | E_COMPILE_ERROR | E_USER_ERROR;

    /** The memory reportFatalErrors keeps for the report of exhausted memory. */
    private const FATAL_RESERVE = 256 * 1024;

    private const JSON_FLAGS = JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE
        | JSON_INVALID_UTF8_SUBSTITUTE;

    /**
     * @param list<string> $frameworkPaths the directories whose code isn't the
     *     module's own: the SDK's and the installed dependencies', each ending in /
     */
    public function __construct(private array $frameworkPaths)
    {
    }

    /**
     * The module's code is everything but the SDK, its generated clients
     * included, and what Composer installed into vendor/. A library the
     * module installs from a path of its own resolves to that path, so its
     * frames stay.
     */
    public static function forModule(string $sdkRoot, string $moduleRoot): self
    {
        $paths = [];
        foreach ([$sdkRoot, "$moduleRoot/vendor"] as $path) {
            $paths[] = rtrim($path, '/') . '/';
            $real = realpath($path);
            if ($real !== false) {
                $paths[] = rtrim($real, '/') . '/';
            }
        }

        return new self(array_values(array_unique($paths)));
    }

    /**
     * Report $error: in the file at $path when there is one and it can be
     * written, else with the marker. Diagnostics go to $stderr. Without the
     * file, the output of a failed exec the error is or wraps goes to $stdout
     * and $stderr first, as an engine that reads no file shows no exec's
     * logs with its error.
     *
     * @param resource $stderr
     * @param ?resource $stdout
     */
    public function report(Throwable $error, ?string $path, $stderr, $stdout = null): void
    {
        if ($path !== null && $path !== '' && self::writeFile($path, $error)) {
            if (!self::hasOrigin($error)) {
                fwrite($stderr, $this->shortTrace($error));
            }
            return;
        }

        $values = self::values($error);
        if ($stdout !== null && is_string($values['stdout'] ?? null)) {
            fwrite($stdout, $values['stdout']);
        }
        fwrite($stderr, sprintf("dagger-module: %s\n", $error));
        if (is_string($values['stderr'] ?? null) && $values['stderr'] !== '') {
            fwrite($stderr, rtrim($values['stderr'], "\n") . "\n");
        }
        self::writeMarker($stderr, self::messageOf($error));
    }

    /**
     * Report a fatal error, which ends the script without a Throwable to
     * catch, as error_get_last() returns it, from a shutdown function. PHP
     * has shown it already, so it is only reported, in the file at $path or
     * else with the marker. Anything other than a fatal error is ignored.
     *
     * @param ?array{type: int, message: string, file: string, line: int} $error
     * @param resource $stderr
     */
    public function reportFatal(?array $error, ?string $path, $stderr): void
    {
        if ($error === null || ($error['type'] & self::FATAL_ERRORS) === 0) {
            return;
        }
        $fatal = new ErrorException($error['message'], 0, $error['type'], $error['file'], $error['line']);
        if ($path !== null && $path !== '' && self::writeFile($path, $fatal)) {
            return;
        }
        self::writeMarker($stderr, self::messageOf($fatal));
    }

    /**
     * Report the fatal error that ends this script, if one does, from a
     * shutdown function (see reportFatal). The memory it reserves is freed
     * first, so that a script that exhausted its memory can still report it.
     *
     * @param resource $stderr
     */
    public function reportFatalErrors(?string $path, $stderr): void
    {
        $reserved = str_repeat("\0", self::FATAL_RESERVE);
        register_shutdown_function(function () use (&$reserved, $path, $stderr): void {
            $reserved = null;
            $this->reportFatal(error_get_last(), $path, $stderr);
        });
    }

    /** @param resource $stderr */
    private static function writeMarker($stderr, string $message): void
    {
        fwrite($stderr, self::MARKER . json_encode(self::truncate($message), self::JSON_FLAGS) . "\n");
    }

    /**
     * Whether the error is an API error that names its origin, the call that
     * failed, which the engine shows with its own logs. An error of the
     * module's own, even one that wraps such an API error, is the module's
     * to show.
     */
    public static function hasOrigin(Throwable $error): bool
    {
        return $error instanceof QueryError && preg_match(self::ORIGIN, $error->getMessage()) === 1;
    }

    /**
     * The report the engine reads: `{"message": ..., "values": {...}}`, the
     * error's message in full, with any origin it names, and the extensions
     * of the GraphQL error it is or wraps, as they are, such as a failed
     * exec's exit code and output. Only a report over what the engine reads
     * is shortened: long strings keep their end, where output usually says
     * what failed, and the message its start and the origins it names; then
     * the values go.
     */
    public static function fileReport(Throwable $error): string
    {
        $message = self::messageOf($error);
        $values = self::values($error);
        $report = self::encodeReport($message, $values);
        if (strlen($report) <= self::FILE_LIMIT) {
            return $report;
        }

        if (strlen($message) > self::SHORTENED_LENGTH) {
            preg_match_all(self::ORIGIN, substr($message, self::SHORTENED_LENGTH), $origins);
            $message = mb_strcut($message, 0, self::SHORTENED_LENGTH) . '...' . implode('', $origins[0]);
        }
        $report = self::encodeReport($message, (array) self::shortened($values));
        if (strlen($report) <= self::FILE_LIMIT) {
            return $report;
        }

        return self::encodeReport($message, []);
    }

    /**
     * The error as PHP prints an uncaught one, through the module's own
     * frames only, previous errors first, and without the origins the
     * messages name. A file that doesn't parse reads as PHP reports it.
     */
    public function shortTrace(Throwable $error): string
    {
        $chain = [];
        for ($e = $error; $e !== null && !in_array($e, $chain, true); $e = $e->getPrevious()) {
            $chain[] = $e;
        }

        $parts = [];
        foreach (array_reverse($chain) as $e) {
            $parts[] = $this->describe($e);
        }

        return implode("\nNext ", $parts);
    }

    private function describe(Throwable $e): string
    {
        $message = self::withoutOrigins($e->getMessage());
        if ($e instanceof CompileError && $this->isModuleFile($e->getFile())) {
            $kind = $e instanceof ParseError ? 'Parse error' : 'Fatal error';
            return sprintf("PHP %s:  %s in %s on line %d\n", $kind, $message, $e->getFile(), $e->getLine());
        }

        $text = $e::class . ($message === '' ? '' : ": $message");
        if ($this->isModuleFile($e->getFile())) {
            $text .= sprintf(' in %s:%d', $e->getFile(), $e->getLine());
        }
        $text .= "\n";

        $frames = [];
        foreach ($e->getTrace() as $frame) {
            $file = $frame['file'] ?? null;
            if ($file === null || !$this->isModuleFile($file)) {
                continue;
            }
            $function = isset($frame['class'])
                ? $frame['class'] . ($frame['type'] ?? '::') . $frame['function']
                : $frame['function'];
            $frames[] = sprintf('#%d %s(%d): %s()', count($frames), $file, $frame['line'] ?? 0, $function);
        }
        if ($frames !== []) {
            $text .= "Stack trace:\n" . implode("\n", $frames) . "\n";
        }

        return $text;
    }

    private function isModuleFile(string $file): bool
    {
        if ($file === '' || !str_starts_with($file, '/')) {
            return false;
        }
        foreach ($this->frameworkPaths as $path) {
            if (str_starts_with($file, $path)) {
                return false;
            }
        }

        return true;
    }

    private static function writeFile(string $path, Throwable $error): bool
    {
        try {
            $report = self::fileReport($error);
        } catch (JsonException) {
            return false;
        }

        return @file_put_contents($path, $report) === strlen($report);
    }

    /**
     * The error's message, or its class when it has none: the engine takes
     * a report with an empty message for plain text.
     */
    private static function messageOf(Throwable $error): string
    {
        $message = $error->getMessage();

        return trim($message) === '' ? $error::class : $message;
    }

    /**
     * The extensions of the GraphQL error that the error is or wraps, or none.
     *
     * @return array<mixed>
     */
    private static function values(Throwable $error): array
    {
        $seen = [];
        for ($e = $error; $e !== null && !in_array($e, $seen, true); $e = $e->getPrevious()) {
            $seen[] = $e;
            if ($e instanceof QueryError) {
                $extensions = $e->getErrorDetails()['extensions'] ?? [];
                return is_array($extensions) ? $extensions : [];
            }
        }

        return [];
    }

    /** @param array<mixed> $values */
    private static function encodeReport(string $message, array $values): string
    {
        return json_encode(
            ['message' => $message, 'values' => $values === [] ? new stdClass() : $values],
            self::JSON_FLAGS,
        );
    }

    /**
     * The value with each string longer than SHORTENED_LENGTH, at any depth,
     * cut to its end.
     */
    private static function shortened(mixed $value): mixed
    {
        if (is_string($value) && strlen($value) > self::SHORTENED_LENGTH) {
            $start = strlen($value) - self::SHORTENED_LENGTH;
            // Not in the middle of a UTF-8 sequence.
            while ($start < strlen($value) && (ord($value[$start]) & 0xC0) === 0x80) {
                $start++;
            }
            return '...' . substr($value, $start);
        }
        if (is_array($value)) {
            return array_map(self::shortened(...), $value);
        }

        return $value;
    }

    private static function truncate(string $message): string
    {
        return strlen($message) <= self::MARKER_LIMIT ? $message : mb_strcut($message, 0, self::MARKER_LIMIT) . '...';
    }

    private static function withoutOrigins(string $message): string
    {
        return (string) preg_replace(self::ORIGIN, '', $message);
    }
}
