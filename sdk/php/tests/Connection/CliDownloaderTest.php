<?php

namespace DaggerIo\Tests\Connection;

use DaggerIo\Connection\CliDownloader;
use DaggerIo\Dagger;
use PHPUnit\Framework\TestCase;

class CliDownloaderTest extends TestCase
{
    public function testCliDownload()
    {
        $versionToDownload = Dagger::DEFAULT_CLI_VERSION;
        $cliDownloader = new CliDownloader($versionToDownload);
        $path = $cliDownloader->download();

        $this->assertNotNull($path);

        $version = shell_exec("{$path} version");

        unlink($path);

        $this->assertStringContainsString($versionToDownload, $version);
    }
}
