<?php

namespace DaggerIo\Connection;

use Composer\InstalledVersions;
use Exception;

final class Provisioning
{
    public static function getCliVersion(): string
    {
        return require_once 'version.php';
    }

    public static function getSdkVersion(): string
    {
        try {
            $version = InstalledVersions::getVersion('dagger/dagger') ?? 'dev';
        } catch (Exception) {
            $version = 'dev';
        }

        return $version;
    }
}
