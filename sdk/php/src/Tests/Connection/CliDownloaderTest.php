<?php

namespace Connection;

use DaggerIo\Connection\CliDownloader;
use DaggerIo\Connection\ProcessSessionDaggerConnection;
use PHPUnit\Framework\TestCase;

class CliDownloaderTest extends TestCase
{
    public function testCliDownload()
    {
        $versionToDownload = '0.9.3';
        $testWorkDir = implode(DIRECTORY_SEPARATOR, [__DIR__, '..', 'Resources', 'workDir']);
        $cliDownloader = new CliDownloader($versionToDownload);
        $path = $cliDownloader->download();

        $this->assertNotNull($path);
        unlink($path);

        $session = new ProcessSessionDaggerConnection($testWorkDir, $cliDownloader);
        $version = $session->getVersion();

        $this->assertStringContainsString($versionToDownload, $version);
    }
}
