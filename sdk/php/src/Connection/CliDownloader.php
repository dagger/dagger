<?php

namespace Dagger\Connection;

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

    private NullLogger $logger;

    public function __construct()
    {
        $this->logger = new NullLogger();
    }

    public function setLogger(LoggerInterface $logger): void
    {
    }

    public function download(string $version = null): string
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

    private function getDefaultCliArchiveName(string $version): string
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

    private function getExpectedChecksum(string $daggerVersion, string $archiveName): ?string
    {
        $checksumMapUrl = "https://dl.dagger.io/dagger/releases/{$daggerVersion}/checksums.txt";
        $checksumMapContent = file_get_contents($checksumMapUrl);

        $checksumArray = explode("\n", trim($checksumMapContent));
        $checksumMap = [];

        foreach ($checksumArray as $checksumLine) {
            [$v, $k] = preg_split("/\s+/", $checksumLine, 2);
            $checksumMap[$k] = $v;
        }

        return $checksumMap[$archiveName] ?? null;
    }

    private function extractCli(string $archiveName, string $daggerVersion, string $tmpBinFile): string
    {
        $tmpArchiveFile = $this->getCacheDir() . DIRECTORY_SEPARATOR . $archiveName;
        $archiveUrl = "https://dl.dagger.io/dagger/releases/{$daggerVersion}/{$archiveName}";
        $this->logger->info("Downloading dagger {$daggerVersion} from {$archiveUrl}");
        file_put_contents($tmpArchiveFile, file_get_contents($archiveUrl));

        if ($this->isWindows()) {
            throw new RuntimeException('Not implemented');
        } else {
            file_put_contents($tmpBinFile, file_get_contents("phar://{$tmpArchiveFile}/dagger"));
        }

        $archiveHash = hash_file('sha256', $tmpArchiveFile);

        unlink($tmpArchiveFile);

        return $archiveHash;
    }
}
