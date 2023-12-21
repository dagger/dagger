<?php

namespace Dagger\Tests\Connection;

use Dagger\Connection\CliDownloader;
use Dagger\Connection\Provisioning;
use PHPUnit\Framework\TestCase;

class CliDownloaderTest extends TestCase
{
    /**
     * @group functional
     */
    public function testRealCliDownload(): void
    {
        $versionToDownload = Provisioning::getCliVersion();
        $cliDownloader = new CliDownloader();
        $path = $cliDownloader->download($versionToDownload);

        $this->assertNotNull($path);

        $version = shell_exec("{$path} version");

        unlink($path);

        $this->assertStringContainsString($versionToDownload, $version);
    }
}
