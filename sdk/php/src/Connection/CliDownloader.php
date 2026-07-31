<?php

namespace Dagger\Connection;

use Dagger\Exception\CliReleaseUnavailable;
use Psr\Log\LoggerAwareInterface;
use Psr\Log\LoggerInterface;
use Psr\Log\NullLogger;
use RuntimeException;

/**
 * @deprecated
 * dagger modules will always have the environment variables set
 * so we don't need to download a CLI Client
 */
class CliDownloader implements LoggerAwareInterface
{
    public const DAGGER_CLI_BIN_PREFIX = 'dagger-';

    private LoggerInterface $logger;

    public function __construct()
    {
        $this->logger = new NullLogger();
    }

    public function setLogger(LoggerInterface $logger): void
    {
        $this->logger = $logger;
    }

    public function download(?string $version = null): string
    {
        if (null === $version) {
            $version = Provisioning::getCliVersion();
        }

        $cacheDir = $this->getCacheDir();
        if (!file_exists($cacheDir)) {
            mkdir($cacheDir);
        }

        $binName = self::DAGGER_CLI_BIN_PREFIX . $version;
        if ($this->isWindows()) {
            $binName .= '.exe';
        }

        $binPath = $cacheDir . DIRECTORY_SEPARATOR . $binName;

        if (file_exists($binPath)) {
            return $binPath;
        }

        $tmpFile = tempnam($cacheDir, 'tmp-');
        $archiveName = $this->getDefaultCliArchiveName($version);
        $expectedChecksum = $this->getExpectedChecksum($version, $archiveName);
        $actualChecksum = $this->extractCli($archiveName, $version, $tmpFile);

        if ($expectedChecksum === $actualChecksum) {
            rename($tmpFile, $binPath);
            chmod($binPath, 0700);
        } else {
            throw new RuntimeException("Invalid checksum : {$actualChecksum}, expected : {$expectedChecksum}");
        }

        return $binPath;
    }

    private function getCacheDir(): string
    {
        $xdgCacheHome = getenv('XDG_CACHE_HOME');
        $rootCacheDir = false !== $xdgCacheHome ? $xdgCacheHome : sys_get_temp_dir();

        return $rootCacheDir . DIRECTORY_SEPARATOR . 'dagger';
    }

    private function isWindows(): bool
    {
        return 'windows' === $this->getOs();
    }

    protected function getDefaultCliArchiveName(string $version): string
    {
        $ext = $this->isWindows() ? 'zip' : 'tar.gz';

        return "dagger_v{$version}_{$this->getOs()}_{$this->getArch()}.{$ext}";
    }

    private function getArch(): string
    {
        $uname = php_uname('m');

        if (str_contains($uname, 'x86_64') || str_contains($uname, 'amd64')) {
            return 'amd64';
        } elseif (str_contains($uname, 'x86')) {
            return 'x86';
        } elseif (str_contains($uname, 'arm')) {
            return 'armv7';
        } elseif (str_contains($uname, 'aarch64')) {
            return 'arm64';
        } else {
            return 'unknown';
        }
    }

    private function getOs(): string
    {
        $os = strtolower(PHP_OS);

        if (str_contains($os, 'win')) {
            return 'windows';
        } elseif (str_contains($os, 'linux')) {
            return 'linux';
        } elseif (str_contains($os, 'darwin')) {
            return 'darwin';
        } else {
            return 'unknown';
        }
    }

    private function getExpectedChecksum(string $daggerVersion, string $archiveName): string
    {
        $checksumMapUrl = $this->getChecksumsUrl($daggerVersion);
        [$checksumMapContent, $statusCode] = $this->fetchUrl($checksumMapUrl);
        if (null !== $statusCode && $statusCode >= 400) {
            $message = "Failed to download checksums from {$checksumMapUrl}: HTTP {$statusCode}";
            if ($this->isCliReleaseUnavailable($statusCode)) {
                throw new CliReleaseUnavailable($message);
            }

            throw new RuntimeException($message);
        }
        if (false === $checksumMapContent) {
            throw new RuntimeException("Failed to download checksums from {$checksumMapUrl}");
        }

        foreach (explode("\n", trim($checksumMapContent)) as $checksumLine) {
            $parts = preg_split('/\s+/', trim($checksumLine), 2);
            if (false === $parts || 2 !== count($parts)) {
                throw new RuntimeException("Malformed checksum data from {$checksumMapUrl}");
            }

            [$checksum, $filename] = $parts;
            if ($filename === $archiveName) {
                if (1 !== preg_match('/^[a-f0-9]{64}$/i', $checksum)) {
                    throw new RuntimeException("Malformed checksum data from {$checksumMapUrl}");
                }

                return $checksum;
            }
        }

        throw new RuntimeException("Could not find checksum for {$archiveName}");
    }

    private function extractCli(string $archiveName, string $daggerVersion, string $tmpBinFile): string
    {
        $tmpArchiveFile = $this->getCacheDir() . DIRECTORY_SEPARATOR . $archiveName;
        $archiveUrl = $this->getArchiveUrl($daggerVersion, $archiveName);
        $this->logger->info("Downloading dagger {$daggerVersion} from {$archiveUrl}");
        [$archiveContent, $statusCode] = $this->fetchUrl($archiveUrl);
        if (null !== $statusCode && $statusCode >= 400) {
            throw new RuntimeException("Failed to download Dagger CLI archive from {$archiveUrl}: HTTP {$statusCode}");
        }
        if (false === $archiveContent) {
            throw new RuntimeException("Failed to download Dagger CLI archive from {$archiveUrl}");
        }
        file_put_contents($tmpArchiveFile, $archiveContent);

        if ($this->isWindows()) {
            throw new RuntimeException('Not implemented');
        } else {
            file_put_contents($tmpBinFile, file_get_contents("phar://{$tmpArchiveFile}/dagger"));
        }

        $archiveHash = hash_file('sha256', $tmpArchiveFile);

        unlink($tmpArchiveFile);

        return $archiveHash;
    }

    private function getChecksumsUrl(string $daggerVersion): string
    {
        return "https://dl.dagger.io/dagger/releases/{$daggerVersion}/checksums.txt";
    }

    private function getArchiveUrl(string $daggerVersion, string $archiveName): string
    {
        return "https://dl.dagger.io/dagger/releases/{$daggerVersion}/{$archiveName}";
    }

    /** @return array{string|false, int|null} */
    protected function fetchUrl(string $url): array
    {
        $context = stream_context_create([
            'http' => [
                'ignore_errors' => true,
            ],
        ]);
        $http_response_header = [];
        $content = @file_get_contents($url, false, $context);
        $responseHeaders = $http_response_header;
        $statusCode = null;

        foreach ($responseHeaders as $header) {
            if (1 === preg_match('/^HTTP\/\S+\s+(\d{3})\b/i', $header, $matches)) {
                $statusCode = (int) $matches[1];
            }
        }

        return [$content, $statusCode];
    }

    private function isCliReleaseUnavailable(int $statusCode): bool
    {
        // dl.dagger.io returns 403 for missing S3 objects.
        return 403 === $statusCode || 404 === $statusCode;
    }
}
