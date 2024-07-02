<?php

namespace Dagger\Tests\Integration\Connection;

use Dagger\Connection\CliDownloader;
use Dagger\Connection\Provisioning;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\TestCase;

#[Group('integration')]
class CliDownloaderTest extends TestCase
{
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
