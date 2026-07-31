<?php

namespace Dagger\Tests\Unit\Connection;

use Dagger\Connection\CliDownloader;
use Dagger\Connection\ProcessSessionConnection;
use Dagger\Connection\Provisioning;
use Dagger\Exception\CliFallbackFailed;
use Dagger\Exception\CliReleaseUnavailable;
use GraphQL\Client;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use Psr\Log\AbstractLogger;
use Psr\Log\LogLevel;
use RuntimeException;
use Stringable;
use Throwable;

#[Group('unit')]
final class CliFallbackTest extends TestCase
{
    private string $tempDir;
    private string|false $previousCacheHome;
    private string|false $previousCliBin;
    private string|false $previousPath;
    private string|false $previousPathExt;

    protected function setUp(): void
    {
        $this->tempDir = sys_get_temp_dir() . DIRECTORY_SEPARATOR . 'dagger-php-test-' . bin2hex(random_bytes(8));
        mkdir($this->tempDir);

        $this->previousCacheHome = getenv('XDG_CACHE_HOME');
        $this->previousCliBin = getenv('_EXPERIMENTAL_DAGGER_CLI_BIN');
        $this->previousPath = getenv('PATH');
        $this->previousPathExt = getenv('PATHEXT');
        putenv("XDG_CACHE_HOME={$this->tempDir}");
        putenv('_EXPERIMENTAL_DAGGER_CLI_BIN');
    }

    protected function tearDown(): void
    {
        $this->restoreEnv('XDG_CACHE_HOME', $this->previousCacheHome);
        $this->restoreEnv('_EXPERIMENTAL_DAGGER_CLI_BIN', $this->previousCliBin);
        $this->restoreEnv('PATH', $this->previousPath);
        $this->restoreEnv('PATHEXT', $this->previousPathExt);
        $this->removeDirectory($this->tempDir);
    }

    #[Test]
    #[DataProvider('unavailableStatusCodes')]
    public function itMarksMissingChecksumMetadataAsUnavailable(int $statusCode): void
    {
        $downloader = new FakeCliDownloader([
            ['', $statusCode],
        ]);

        $error = $this->catchError(fn() => $downloader->download('unreleased'));

        self::assertInstanceOf(CliReleaseUnavailable::class, $error);
        self::assertStringContainsString("HTTP {$statusCode}", $error->getMessage());
    }

    #[Test]
    #[DataProvider('unavailableStatusCodes')]
    public function itDoesNotMarkMissingArchivesAsUnavailable(int $statusCode): void
    {
        // Missing checksums mean the release is absent; a missing archive may be a
        // partial or broken release, so it must remain fatal.
        $downloader = new FakeCliDownloader([]);
        $archiveName = $downloader->archiveName('unreleased');
        $downloader->setResponses([
            [str_repeat('0', 64) . "  {$archiveName}\n", 200],
            ['', $statusCode],
        ]);

        $error = $this->catchError(fn() => $downloader->download('unreleased'));

        self::assertNotInstanceOf(CliReleaseUnavailable::class, $error);
        self::assertStringContainsString("HTTP {$statusCode}", $error->getMessage());
    }

    #[Test]
    public function itValidatesChecksumMetadataBeforeDownloadingTheArchive(): void
    {
        $downloader = new FakeCliDownloader([
            [str_repeat('0', 64) . "  another-archive.tar.gz\n", 200],
            new RuntimeException('archive should not be downloaded'),
        ]);

        $error = $this->catchError(fn() => $downloader->download('unreleased'));

        self::assertNotInstanceOf(CliReleaseUnavailable::class, $error);
        self::assertStringContainsString('Could not find checksum', $error->getMessage());
    }

    #[Test]
    public function itDoesNotMarkOtherChecksumHttpErrorsAsUnavailable(): void
    {
        $downloader = new FakeCliDownloader([
            ['', 500],
        ]);

        $error = $this->catchError(fn() => $downloader->download('unreleased'));

        self::assertNotInstanceOf(CliReleaseUnavailable::class, $error);
        self::assertStringContainsString('HTTP 500', $error->getMessage());
    }

    #[Test]
    public function itDoesNotFallBackForUnrelatedDownloadErrors(): void
    {
        $transportError = new RuntimeException('transport failed');
        $downloader = new FakeCliDownloader([$transportError]);
        $downloadError = $this->catchError(fn() => $downloader->download('unreleased'));
        $connection = $this->newConnection();

        $fallbackError = $this->catchError(fn() => $connection->fallbackToLocalCli($downloadError));

        self::assertSame($transportError, $downloadError);
        self::assertSame($downloadError, $fallbackError);
    }

    #[Test]
    public function itUsesTheDaggerCliInPathAndWarns(): void
    {
        $binPath = $this->createDaggerExecutable();
        $downloadError = new CliReleaseUnavailable('download failed');
        $logger = new CollectingLogger();
        $connection = $this->newConnection();
        $connection->setLogger($logger);
        $cliVersion = Provisioning::getCliVersion();

        $actual = $connection->fallbackToLocalCli($downloadError);

        self::assertSame($binPath, $actual);
        self::assertSame([
            [
                LogLevel::WARNING,
                sprintf(
                    'CLI version %s is unavailable; using %s from PATH (version compatibility is not guaranteed).',
                    $cliVersion,
                    $binPath,
                ),
            ],
        ], $logger->records);
    }

    #[Test]
    public function itWarnsOnStderrWithTheDefaultLogger(): void
    {
        $binPath = $this->createDaggerExecutable();
        $downloadError = new CliReleaseUnavailable('download failed');
        $connection = new CapturingWarningProcessSessionConnection(
            $this->tempDir,
            false,
            new CliDownloader(),
        );

        $actual = $connection->fallbackToLocalCli($downloadError);

        self::assertSame($binPath, $actual);
        self::assertSame(
            sprintf(
                'CLI version %s is unavailable; using %s from PATH (version compatibility is not guaranteed).%s',
                Provisioning::getCliVersion(),
                $binPath,
                PHP_EOL,
            ),
            $connection->warning(),
        );
    }

    #[Test]
    public function itPreservesDownloadAndPathErrorsWhenNoCliIsFound(): void
    {
        $emptyPath = $this->tempDir . DIRECTORY_SEPARATOR . 'empty';
        mkdir($emptyPath);
        putenv("PATH={$emptyPath}");
        $downloadError = new CliReleaseUnavailable('download failed');

        $error = $this->catchError(fn() => $this->newConnection()->fallbackToLocalCli($downloadError));

        self::assertInstanceOf(CliFallbackFailed::class, $error);
        self::assertSame($downloadError, $error->getDownloadError());
        self::assertSame($error->getFallbackError(), $error->getPrevious());
        self::assertInstanceOf(RuntimeException::class, $error->getFallbackError());
        self::assertSame('dagger executable was not found', $error->getFallbackError()->getMessage());
        self::assertStringContainsString('download failed', (string) $error);
        self::assertStringContainsString('dagger executable was not found', (string) $error);
    }

    #[Test]
    public function itRefusesADaggerCliFoundRelativeToTheCurrentDirectory(): void
    {
        $this->createDaggerExecutable();
        $previousCwd = getcwd();
        self::assertNotFalse($previousCwd);
        chdir($this->tempDir);
        putenv('PATH=bin');
        $downloadError = new CliReleaseUnavailable('download failed');

        try {
            $error = $this->catchError(fn() => $this->newConnection()->fallbackToLocalCli($downloadError));
        } finally {
            chdir($previousCwd);
        }

        self::assertInstanceOf(CliFallbackFailed::class, $error);
        self::assertSame($downloadError, $error->getDownloadError());
        self::assertStringContainsString('relative to the current directory', (string) $error);
    }

    #[Test]
    public function itPreservesDownloadAndSessionErrorsWhenTheFallbackFailsToStart(): void
    {
        $binPath = $this->createDaggerExecutable();
        $sessionError = new RuntimeException('session failed');
        $downloader = new FakeCliDownloader([
            ['', 403],
        ]);
        $connection = new FailingProcessSessionConnection(
            $this->tempDir,
            false,
            $downloader,
            $sessionError,
        );
        $connection->setLogger(new CollectingLogger());

        $error = $this->catchError(fn() => $connection->connect());

        self::assertInstanceOf(CliFallbackFailed::class, $error);
        self::assertInstanceOf(CliReleaseUnavailable::class, $error->getDownloadError());
        self::assertSame($sessionError, $error->getFallbackError());
        self::assertSame($sessionError, $error->getPrevious());
        self::assertStringContainsString('Failed to download checksums', (string) $error);
        self::assertStringContainsString("failed to use CLI from PATH \"{$binPath}\": session failed", (string) $error);
    }

    /** @return array<string, array{int}> */
    public static function unavailableStatusCodes(): array
    {
        return [
            '403 Forbidden' => [403],
            '404 Not Found' => [404],
        ];
    }

    private function newConnection(): ProcessSessionConnection
    {
        return new ProcessSessionConnection($this->tempDir, false, new CliDownloader());
    }

    private function createDaggerExecutable(): string
    {
        $binDir = $this->tempDir . DIRECTORY_SEPARATOR . 'bin';
        mkdir($binDir);
        $isWindows = '\\' === DIRECTORY_SEPARATOR;
        $binPath = $binDir . DIRECTORY_SEPARATOR . ($isWindows ? 'dagger.bat' : 'dagger');
        file_put_contents($binPath, $isWindows ? "@exit /b 1\r\n" : "#!/bin/sh\nexit 1\n");
        chmod($binPath, 0700);
        putenv("PATH={$binDir}");
        if ($isWindows) {
            putenv('PATHEXT=.BAT');
        }

        return $binPath;
    }

    private function catchError(callable $operation): Throwable
    {
        try {
            $operation();
        } catch (Throwable $error) {
            return $error;
        }

        self::fail('Expected operation to fail');
    }

    private function restoreEnv(string $name, string|false $value): void
    {
        putenv(false === $value ? $name : "{$name}={$value}");
    }

    private function removeDirectory(string $directory): void
    {
        $files = new \RecursiveIteratorIterator(
            new \RecursiveDirectoryIterator($directory, \FilesystemIterator::SKIP_DOTS),
            \RecursiveIteratorIterator::CHILD_FIRST,
        );
        foreach ($files as $file) {
            $file->isDir() ? rmdir($file->getPathname()) : unlink($file->getPathname());
        }
        rmdir($directory);
    }
}

final class FakeCliDownloader extends CliDownloader
{
    /** @param list<array{string|false, int|null}|Throwable> $responses */
    public function __construct(private array $responses)
    {
        parent::__construct();
    }

    /** @param list<array{string|false, int|null}|Throwable> $responses */
    public function setResponses(array $responses): void
    {
        $this->responses = $responses;
    }

    public function archiveName(string $version): string
    {
        return $this->getDefaultCliArchiveName($version);
    }

    /** @return array{string|false, int|null} */
    protected function fetchUrl(string $url): array
    {
        $response = array_shift($this->responses);
        if ($response instanceof Throwable) {
            throw $response;
        }
        if (null === $response) {
            throw new RuntimeException("Unexpected request for {$url}");
        }

        return $response;
    }
}

final class FailingProcessSessionConnection extends ProcessSessionConnection
{
    public function __construct(
        string $workDir,
        bool $loadWorkspaceModules,
        CliDownloader $cliDownloader,
        private readonly Throwable $sessionError,
    ) {
        parent::__construct($workDir, $loadWorkspaceModules, $cliDownloader);
    }

    protected function startCliSession(string $cliBinPath): Client
    {
        throw $this->sessionError;
    }
}

final class CapturingWarningProcessSessionConnection extends ProcessSessionConnection
{
    /** @var resource */
    private $warningStream;

    public function __construct(
        string $workDir,
        bool $loadWorkspaceModules,
        CliDownloader $cliDownloader,
    ) {
        $stream = fopen('php://memory', 'w+');
        if (false === $stream) {
            throw new RuntimeException('Could not create warning stream');
        }
        $this->warningStream = $stream;
        parent::__construct($workDir, $loadWorkspaceModules, $cliDownloader);
    }

    public function warning(): string
    {
        rewind($this->warningStream);
        $warning = stream_get_contents($this->warningStream);
        if (false === $warning) {
            throw new RuntimeException('Could not read warning stream');
        }

        return $warning;
    }

    /** @return resource */
    protected function warningOutput()
    {
        return $this->warningStream;
    }
}

final class CollectingLogger extends AbstractLogger
{
    /** @var list<array{mixed, string}> */
    public array $records = [];

    public function log($level, Stringable|string $message, array $context = []): void
    {
        $this->records[] = [$level, (string) $message];
    }
}
